// file: internal/service/workflow/service.go

// Package workflow 负责工作流的定义、构建和执行。
// 它将数据库中存储的流程图，动态地构建成内存中可执行的 Aegis 工作流实例。
package workflow

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/jmespath/go-jmespath"
)

// FullWorkflowPayload 是一个用于API层的复合结构，封装了工作流的完整定义。
type FullWorkflowPayload struct {
	Workflow domain.Workflow       `json:"workflow"`
	Nodes    []domain.WorkflowNode `json:"nodes"`
	Edges    []domain.WorkflowEdge `json:"edges"`
}

// Service 负责工作流的CRUD和执行。
type Service struct {
	db               *sql.DB
	executorProvider port.PluginExecutorProvider
	celEnv           *cel.Env
}

// NewService 创建一个新的 Service 实例。
func NewService(db *sql.DB, provider port.PluginExecutorProvider) (*Service, error) {
	env, err := cel.NewEnv(
		cel.Variable("result", cel.AnyType),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化CEL环境失败: %w", err)
	}
	return &Service{db: db, executorProvider: provider, celEnv: env}, nil
}

// =============================================================================
//  工作流执行 (Execution)
// =============================================================================

// RunWorkflow 是执行工作流的统一入口。
func (s *Service) RunWorkflow(ctx context.Context, flowID string, initialParams map[string]any) (string, error) {
	runnable, err := s.buildAegisRunnable(ctx, flowID, initialParams)
	if err != nil {
		return "", fmt.Errorf("无法构建工作流 '%s' 的可执行实例: %w", flowID, err)
	}
	return runnable.Execute(ctx)
}

// buildAegisRunnable 从数据库加载定义，并构建一个内存中的 AegisWorkflowRunnable 对象。
func (s *Service) buildAegisRunnable(ctx context.Context, flowID string, initialParams map[string]any) (*AegisWorkflowRunnable, error) {
	payload, err := s.getWorkflowInternal(ctx, flowID)
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]AegisNode)
	for _, nodeRecord := range payload.Nodes {
		var node AegisNode
		var err error
		switch nodeRecord.NodeType {
		case "PLUGIN":
			node, err = s.createAegisPluginNode(nodeRecord.ID, nodeRecord.Config)
		case "DATA_TRANSFORM":
			node, err = s.createAegisDataTransformNode(nodeRecord.ID, nodeRecord.Config)
		default:
			return nil, fmt.Errorf("构建流程失败: 节点 '%s' 有一个不支持的类型 '%s'", nodeRecord.ID, nodeRecord.NodeType)
		}
		if err != nil {
			return nil, fmt.Errorf("创建节点 '%s' 失败: %w", nodeRecord.ID, err)
		}
		nodes[nodeRecord.ID] = node
	}

	for _, edgeRecord := range payload.Edges {
		sourceNode, okS := nodes[edgeRecord.SourceNodeID]
		targetNode, okT := nodes[edgeRecord.TargetNodeID]
		if !okS || !okT {
			return nil, fmt.Errorf("构建流程图失败: 找不到边 %v 的源或目标节点", edgeRecord.ID)
		}
		var conditionFunc AegisConditionFunc
		if len(edgeRecord.Condition) > 0 && string(edgeRecord.Condition) != "null" {
			var condDef struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(edgeRecord.Condition, &condDef); err != nil {
				return nil, fmt.Errorf("解析边 %v 的条件JSON失败: %w", edgeRecord.ID, err)
			}
			if condDef.Expression == "" {
				continue
			}
			ast, issues := s.celEnv.Compile(condDef.Expression)
			if issues != nil && issues.Err() != nil {
				return nil, fmt.Errorf("编译边 %v 的条件表达式 '%s' 失败: %w", condDef.Expression, issues.Err())
			}
			prg, err := s.celEnv.Program(ast)
			if err != nil {
				return nil, fmt.Errorf("创建边 %v 的条件程序失败: %w", edgeRecord.ID, err)
			}
			conditionFunc = func(params map[string]any) (bool, error) {
				out, _, err := prg.Eval(params)
				if err != nil {
					return false, fmt.Errorf("执行边 %v 的条件失败: %w", edgeRecord.ID, err)
				}
				result, ok := out.Value().(bool)
				if !ok {
					return false, fmt.Errorf("边 %v 的条件表达式未返回布尔值", edgeRecord.ID)
				}
				return result, nil
			}
		}
		edge := &AegisEdge{
			TargetNode:    targetNode,
			Action:        edgeRecord.Action,
			ConditionFunc: conditionFunc,
		}
		sourceNode.AddEdge(edge)
	}

	startNode, ok := nodes[payload.Workflow.StartNodeID]
	if !ok {
		return nil, fmt.Errorf("未找到流程的起始节点 '%s'", payload.Workflow.StartNodeID)
	}

	return NewAegisWorkflowRunnable(startNode, initialParams), nil
}

// =============================================================================
//  工作流管理 (Admin CRUD)
// =============================================================================

// CreateWorkflow 在数据库中创建一个新的完整工作流定义。
func (s *Service) CreateWorkflow(ctx context.Context, workflow domain.Workflow, nodes []domain.WorkflowNode, edges []domain.WorkflowEdge) (*FullWorkflowPayload, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	// 如果没有提供ID，则生成一个新的UUID
	if workflow.ID == "" {
		workflow.ID = uuid.New().String()
	}

	// 插入主记录
	const insertWorkflow = `INSERT INTO workflows (id, name, description, trigger_type, start_node_id) VALUES (?, ?, ?, ?, ?)`
	_, err = tx.ExecContext(ctx, insertWorkflow, workflow.ID, workflow.Name, workflow.Description, workflow.TriggerType, workflow.StartNodeID)
	if err != nil {
		return nil, fmt.Errorf("插入工作流主记录失败: %w", err)
	}

	// 插入节点
	const insertNode = `INSERT INTO workflow_nodes (id, workflow_id, name, node_type, config_json) VALUES (?, ?, ?, ?, ?)`
	for _, node := range nodes {
		node.WorkflowID = workflow.ID // 确保关联正确
		_, err := tx.ExecContext(ctx, insertNode, node.ID, node.WorkflowID, node.Name, node.NodeType, node.Config)
		if err != nil {
			return nil, fmt.Errorf("插入节点 '%s' 失败: %w", node.Name, err)
		}
	}

	// 插入边
	const insertEdge = `INSERT INTO workflow_edges (workflow_id, source_node_id, target_node_id, action, condition_json) VALUES (?, ?, ?, ?, ?)`
	for _, edge := range edges {
		edge.WorkflowID = workflow.ID // 确保关联正确
		_, err := tx.ExecContext(ctx, insertEdge, edge.WorkflowID, edge.SourceNodeID, edge.TargetNodeID, edge.Action, edge.Condition)
		if err != nil {
			return nil, fmt.Errorf("插入从 '%s' 到 '%s' 的边失败: %w", edge.SourceNodeID, edge.TargetNodeID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	slog.Info("成功创建工作流", "id", workflow.ID, "name", workflow.Name)
	return &FullWorkflowPayload{Workflow: workflow, Nodes: nodes, Edges: edges}, nil
}

// ListWorkflows 返回所有已定义的工作流（仅基础信息）。
func (s *Service) ListWorkflows(ctx context.Context) ([]domain.Workflow, error) {
	const query = `SELECT id, name, description, trigger_type, start_node_id FROM workflows ORDER BY name`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("查询工作流列表失败: %w", err)
	}
	defer rows.Close()

	var workflows []domain.Workflow
	for rows.Next() {
		var wf domain.Workflow
		if err := rows.Scan(&wf.ID, &wf.Name, &wf.Description, &wf.TriggerType, &wf.StartNodeID); err != nil {
			return nil, fmt.Errorf("扫描工作流数据失败: %w", err)
		}
		workflows = append(workflows, wf)
	}
	return workflows, rows.Err()
}

// GetWorkflow 返回一个工作流的完整定义（包括节点和边）。
func (s *Service) GetWorkflow(ctx context.Context, workflowID string) (*FullWorkflowPayload, error) {
	return s.getWorkflowInternal(ctx, workflowID)
}

// UpdateWorkflow 更新一个已存在的工作流。采用“先删除旧的子记录，再插入新的”策略。
func (s *Service) UpdateWorkflow(ctx context.Context, workflow domain.Workflow, nodes []domain.WorkflowNode, edges []domain.WorkflowEdge) (*FullWorkflowPayload, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启更新事务失败: %w", err)
	}
	defer tx.Rollback()

	// 更新主记录
	const updateWorkflow = `UPDATE workflows SET name = ?, description = ?, trigger_type = ?, start_node_id = ? WHERE id = ?`
	res, err := tx.ExecContext(ctx, updateWorkflow, workflow.Name, workflow.Description, workflow.TriggerType, workflow.StartNodeID, workflow.ID)
	if err != nil {
		return nil, fmt.Errorf("更新工作流主记录失败: %w", err)
	}
	if rowsAffected, _ := res.RowsAffected(); rowsAffected == 0 {
		return nil, fmt.Errorf("未找到要更新的工作流: %s", workflow.ID)
	}

	// 删除旧的节点和边
	const deleteNodes = `DELETE FROM workflow_nodes WHERE workflow_id = ?`
	if _, err := tx.ExecContext(ctx, deleteNodes, workflow.ID); err != nil {
		return nil, fmt.Errorf("删除旧节点失败: %w", err)
	}
	const deleteEdges = `DELETE FROM workflow_edges WHERE workflow_id = ?`
	if _, err := tx.ExecContext(ctx, deleteEdges, workflow.ID); err != nil {
		return nil, fmt.Errorf("删除旧边失败: %w", err)
	}

	// 插入新的节点
	const insertNode = `INSERT INTO workflow_nodes (id, workflow_id, name, node_type, config_json) VALUES (?, ?, ?, ?, ?)`
	for _, node := range nodes {
		node.WorkflowID = workflow.ID
		if _, err := tx.ExecContext(ctx, insertNode, node.ID, node.WorkflowID, node.Name, node.NodeType, node.Config); err != nil {
			return nil, fmt.Errorf("插入新节点 '%s' 失败: %w", node.Name, err)
		}
	}

	// 插入新的边
	const insertEdge = `INSERT INTO workflow_edges (workflow_id, source_node_id, target_node_id, action, condition_json) VALUES (?, ?, ?, ?, ?)`
	for _, edge := range edges {
		edge.WorkflowID = workflow.ID
		if _, err := tx.ExecContext(ctx, insertEdge, edge.WorkflowID, edge.SourceNodeID, edge.TargetNodeID, edge.Action, edge.Condition); err != nil {
			return nil, fmt.Errorf("插入新边 '%s' -> '%s' 失败: %w", edge.SourceNodeID, edge.TargetNodeID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交更新事务失败: %w", err)
	}

	slog.Info("成功更新工作流", "id", workflow.ID, "name", workflow.Name)
	return &FullWorkflowPayload{Workflow: workflow, Nodes: nodes, Edges: edges}, nil
}

// DeleteWorkflow 删除一个工作流及其所有相关节点和边。
func (s *Service) DeleteWorkflow(ctx context.Context, workflowID string) error {
	// 由于外键设置了 ON DELETE CASCADE，我们只需要删除主记录即可。
	const query = `DELETE FROM workflows WHERE id = ?`
	res, err := s.db.ExecContext(ctx, query, workflowID)
	if err != nil {
		return fmt.Errorf("删除工作流 '%s' 失败: %w", workflowID, err)
	}
	if rowsAffected, _ := res.RowsAffected(); rowsAffected == 0 {
		return fmt.Errorf("未找到要删除的工作流: %s", workflowID)
	}
	slog.Info("成功删除工作流", "id", workflowID)
	return nil
}

// getWorkflowInternal 是一个内部辅助函数，用于在一个事务中获取完整的工作流定义。
func (s *Service) getWorkflowInternal(ctx context.Context, workflowID string) (*FullWorkflowPayload, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("开启只读事务失败: %w", err)
	}
	defer tx.Rollback()

	var wf domain.Workflow
	queryWorkflow := `SELECT id, name, description, trigger_type, start_node_id FROM workflows WHERE id = ?`
	if err := tx.QueryRowContext(ctx, queryWorkflow, workflowID).Scan(&wf.ID, &wf.Name, &wf.Description, &wf.TriggerType, &wf.StartNodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("未找到 ID 为 '%s' 的工作流", workflowID)
		}
		return nil, fmt.Errorf("查询工作流 '%s' 失败: %w", workflowID, err)
	}

	var nodes []domain.WorkflowNode
	queryNodes := `SELECT id, workflow_id, name, node_type, config_json FROM workflow_nodes WHERE workflow_id = ?`
	rowsNodes, err := tx.QueryContext(ctx, queryNodes, workflowID)
	if err != nil {
		return nil, fmt.Errorf("查询节点失败: %w", err)
	}
	defer rowsNodes.Close()
	for rowsNodes.Next() {
		var node domain.WorkflowNode
		if err := rowsNodes.Scan(&node.ID, &node.WorkflowID, &node.Name, &node.NodeType, &node.Config); err != nil {
			return nil, fmt.Errorf("扫描节点数据失败: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rowsNodes.Err(); err != nil {
		return nil, fmt.Errorf("遍历节点时出错: %w", err)
	}

	var edges []domain.WorkflowEdge
	queryEdges := `SELECT id, workflow_id, source_node_id, target_node_id, action, condition_json FROM workflow_edges WHERE workflow_id = ?`
	rowsEdges, err := tx.QueryContext(ctx, queryEdges, workflowID)
	if err != nil {
		return nil, fmt.Errorf("查询边失败: %w", err)
	}
	defer rowsEdges.Close()
	for rowsEdges.Next() {
		var edge domain.WorkflowEdge
		if err := rowsEdges.Scan(&edge.ID, &edge.WorkflowID, &edge.SourceNodeID, &edge.TargetNodeID, &edge.Action, &edge.Condition); err != nil {
			return nil, fmt.Errorf("扫描边数据失败: %w", err)
		}
		edges = append(edges, edge)
	}
	if err := rowsEdges.Err(); err != nil {
		return nil, fmt.Errorf("遍历边时出错: %w", err)
	}

	return &FullWorkflowPayload{Workflow: wf, Nodes: nodes, Edges: edges}, nil
}

// =============================================================================
//  工作流细化管理 (Granular Admin CRUD)
// =============================================================================

// --- Node Methods ---

// AddNode 向现有工作流中添加一个新节点。
func (s *Service) AddNode(ctx context.Context, workflowID string, node domain.WorkflowNode) (*domain.WorkflowNode, error) {
	node.WorkflowID = workflowID
	if node.ID == "" {
		node.ID = uuid.New().String()
	}

	const query = `INSERT INTO workflow_nodes (id, workflow_id, name, node_type, config_json) VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, node.ID, node.WorkflowID, node.Name, node.NodeType, node.Config)
	if err != nil {
		return nil, fmt.Errorf("向工作流 '%s' 添加节点 '%s' 失败: %w", workflowID, node.Name, err)
	}

	return &node, nil
}

// UpdateNode 更新单个节点的配置（目前只允许更新名称和配置）。
func (s *Service) UpdateNode(ctx context.Context, workflowID string, nodeID string, node domain.WorkflowNode) (*domain.WorkflowNode, error) {
	const query = `UPDATE workflow_nodes SET name = ?, config_json = ? WHERE id = ? AND workflow_id = ?`
	res, err := s.db.ExecContext(ctx, query, node.Name, node.Config, nodeID, workflowID)
	if err != nil {
		return nil, fmt.Errorf("更新节点 '%s' 失败: %w", nodeID, err)
	}
	if rowsAffected, _ := res.RowsAffected(); rowsAffected == 0 {
		return nil, fmt.Errorf("未找到要更新的节点: %s", nodeID)
	}
	node.ID = nodeID
	node.WorkflowID = workflowID
	return &node, nil
}

// DeleteNode 删除单个节点，并清除所有与之相关的边。
func (s *Service) DeleteNode(ctx context.Context, workflowID string, nodeID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启删除节点事务失败: %w", err)
	}
	defer tx.Rollback()

	// 1. 删除所有指向或来自该节点的边
	const deleteEdges = `DELETE FROM workflow_edges WHERE workflow_id = ? AND (source_node_id = ? OR target_node_id = ?)`
	if _, err := tx.ExecContext(ctx, deleteEdges, workflowID, nodeID, nodeID); err != nil {
		return fmt.Errorf("删除节点 '%s' 的关联边失败: %w", nodeID, err)
	}

	// 2. 删除节点本身
	const deleteNode = `DELETE FROM workflow_nodes WHERE id = ? AND workflow_id = ?`
	res, err := tx.ExecContext(ctx, deleteNode, nodeID, workflowID)
	if err != nil {
		return fmt.Errorf("删除节点 '%s' 失败: %w", nodeID, err)
	}
	if rowsAffected, _ := res.RowsAffected(); rowsAffected == 0 {
		return fmt.Errorf("未找到要删除的节点: %s", nodeID)
	}

	return tx.Commit()
}

// --- Edge Methods ---

// AddEdge 向现有工作流中添加一条新边。
func (s *Service) AddEdge(ctx context.Context, workflowID string, edge domain.WorkflowEdge) (*domain.WorkflowEdge, error) {
	edge.WorkflowID = workflowID
	const query = `INSERT INTO workflow_edges (workflow_id, source_node_id, target_node_id, action, condition_json) VALUES (?, ?, ?, ?, ?)`
	res, err := s.db.ExecContext(ctx, query, edge.WorkflowID, edge.SourceNodeID, edge.TargetNodeID, edge.Action, edge.Condition)
	if err != nil {
		return nil, fmt.Errorf("添加从 '%s' 到 '%s' 的边失败: %w", edge.SourceNodeID, edge.TargetNodeID, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("获取新边的ID失败: %w", err)
	}
	edge.ID = id
	return &edge, nil
}

// UpdateEdge 更新单条边的配置。
func (s *Service) UpdateEdge(ctx context.Context, workflowID string, edgeID int64, edge domain.WorkflowEdge) (*domain.WorkflowEdge, error) {
	const query = `UPDATE workflow_edges SET source_node_id = ?, target_node_id = ?, action = ?, condition_json = ? WHERE id = ? AND workflow_id = ?`
	res, err := s.db.ExecContext(ctx, query, edge.SourceNodeID, edge.TargetNodeID, edge.Action, edge.Condition, edgeID, workflowID)
	if err != nil {
		return nil, fmt.Errorf("更新边 '%d' 失败: %w", edgeID, err)
	}
	if rowsAffected, _ := res.RowsAffected(); rowsAffected == 0 {
		return nil, fmt.Errorf("未找到要更新的边: %d", edgeID)
	}
	edge.ID = edgeID
	edge.WorkflowID = workflowID
	return &edge, nil
}

// DeleteEdge 删除单条边。
func (s *Service) DeleteEdge(ctx context.Context, workflowID string, edgeID int64) error {
	const query = `DELETE FROM workflow_edges WHERE id = ? AND workflow_id = ?`
	res, err := s.db.ExecContext(ctx, query, edgeID, workflowID)
	if err != nil {
		return fmt.Errorf("删除边 '%d' 失败: %w", edgeID, err)
	}
	if rowsAffected, _ := res.RowsAffected(); rowsAffected == 0 {
		return fmt.Errorf("未找到要删除的边: %d", edgeID)
	}
	return nil
}

// =============================================================================
// 节点创建辅助函数 (Node Factory Helper Functions)
// =============================================================================

// dataTransformNodeConfig 定义了 DATA_TRANSFORM 类型节点的配置结构。
type dataTransformNodeConfig struct {
	Mappings map[string]string `json:"mappings"`
}

// createAegisDataTransformNode 根据配置创建一个数据转换节点。
func (s *Service) createAegisDataTransformNode(nodeID string, configData json.RawMessage) (AegisNode, error) {
	var config dataTransformNodeConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("解析数据转换节点 '%s' 配置失败: %w", nodeID, err)
	}

	compiledMappings := make(map[string]*jmespath.JMESPath)
	for key, expr := range config.Mappings {
		compiled, err := jmespath.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("编译节点 '%s' 的 JMESPath 表达式 '%s' 失败: %w", expr, err)
		}
		compiledMappings[key] = compiled
	}

	node := &AegisDataTransformNode{
		aegisNodeCore:    aegisNodeCore{id: nodeID},
		compiledMappings: compiledMappings,
	}

	return node, nil
}
