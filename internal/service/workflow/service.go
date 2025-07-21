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

	"github.com/google/cel-go/cel"
	"github.com/jmespath/go-jmespath"
)

// Service 负责工作流的CRUD和执行。
type Service struct {
	db               *sql.DB
	executorProvider port.PluginExecutorProvider
	celEnv           *cel.Env // <-- 修改 #3：将类型改为指针 *cel.Env
}

// NewWorkflowService 创建一个新的 Service 实例。
func NewWorkflowService(db *sql.DB, provider port.PluginExecutorProvider) (*Service, error) {
	env, err := cel.NewEnv(
		cel.Variable("result", cel.AnyType),
	)
	if err != nil {
		return nil, fmt.Errorf("初始化CEL环境失败: %w", err)
	}

	return &Service{db: db, executorProvider: provider, celEnv: env}, nil
}

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
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("开启数据库事务失败: %w", err)
	}
	defer tx.Rollback()

	var workflowRecord domain.Workflow
	queryWorkflow := `SELECT id, name, description, trigger_type, start_node_id FROM workflows WHERE id = ?`
	if err := tx.QueryRowContext(ctx, queryWorkflow, flowID).Scan(
		&workflowRecord.ID, &workflowRecord.Name, &workflowRecord.Description,
		&workflowRecord.TriggerType, &workflowRecord.StartNodeID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("未找到 ID 为 '%s' 的工作流", flowID)
		}
		return nil, fmt.Errorf("查询工作流 '%s' 失败: %w", flowID, err)
	}

	var dbNodes []domain.WorkflowNode
	queryNodes := `SELECT id, workflow_id, name, node_type, config_json FROM workflow_nodes WHERE workflow_id = ?`
	rowsNodes, err := tx.QueryContext(ctx, queryNodes, flowID)
	if err != nil {
		return nil, fmt.Errorf("查询工作流 '%s' 的节点失败: %w", flowID, err)
	}
	defer rowsNodes.Close()
	for rowsNodes.Next() {
		var node domain.WorkflowNode
		if err := rowsNodes.Scan(&node.ID, &node.WorkflowID, &node.Name, &node.NodeType, &node.Config); err != nil {
			return nil, fmt.Errorf("扫描节点数据失败: %w", err)
		}
		dbNodes = append(dbNodes, node)
	}
	if err := rowsNodes.Err(); err != nil {
		return nil, fmt.Errorf("遍历节点查询结果时出错: %w", err)
	}

	var dbEdges []domain.WorkflowEdge
	queryEdges := `SELECT id, workflow_id, source_node_id, target_node_id, action, condition_json FROM workflow_edges WHERE workflow_id = ?`
	rowsEdges, err := tx.QueryContext(ctx, queryEdges, flowID)
	if err != nil {
		return nil, fmt.Errorf("查询工作流 '%s' 的边失败: %w", flowID, err)
	}
	defer rowsEdges.Close()
	for rowsEdges.Next() {
		var edge domain.WorkflowEdge
		if err := rowsEdges.Scan(&edge.ID, &edge.WorkflowID, &edge.SourceNodeID, &edge.TargetNodeID, &edge.Action, &edge.Condition); err != nil {
			return nil, fmt.Errorf("扫描边数据失败: %w", err)
		}
		dbEdges = append(dbEdges, edge)
	}
	if err := rowsEdges.Err(); err != nil {
		return nil, fmt.Errorf("遍历边查询结果时出错: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交数据库事务失败: %w", err)
	}

	nodes := make(map[string]AegisNode)
	for _, nodeRecord := range dbNodes {
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

	for _, edgeRecord := range dbEdges {
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
				return nil, fmt.Errorf("编译边 %v 的条件表达式 '%s' 失败: %w", edgeRecord.ID, condDef.Expression, issues.Err())
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

	startNode, ok := nodes[workflowRecord.StartNodeID]
	if !ok {
		return nil, fmt.Errorf("未找到流程的起始节点 '%s'", workflowRecord.StartNodeID)
	}

	return NewAegisWorkflowRunnable(startNode, initialParams), nil
}

// -----------------------------------------------------------------------------
// 节点创建辅助函数 (Node Factory Helper Functions)
// -----------------------------------------------------------------------------

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
			return nil, fmt.Errorf("编译节点 '%s' 的 JMESPath 表达式 '%s' 失败: %w", nodeID, expr, err)
		}
		compiledMappings[key] = compiled
	}

	node := &AegisDataTransformNode{
		aegisNodeCore:    aegisNodeCore{id: nodeID},
		compiledMappings: compiledMappings,
	}

	return node, nil
}
