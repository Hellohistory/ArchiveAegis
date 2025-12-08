// file: internal/service/workflow/snapshot.go

package workflow

import (
	"context"
	"fmt"
)

// Snapshot 捕获所有工作流、节点和边的定义，用于 Raft 快照。
type Snapshot struct {
	Workflows []FullWorkflowPayload `json:"workflows"`
}

// SnapshotState 导出当前的工作流定义列表。
func (s *Service) SnapshotState(ctx context.Context) (*Snapshot, error) {
	workflows, err := s.ListWorkflows(ctx)
	if err != nil {
		return nil, err
	}

	var payloads []FullWorkflowPayload
	for _, wf := range workflows {
		payload, err := s.GetWorkflow(ctx, wf.ID)
		if err != nil {
			return nil, fmt.Errorf("获取工作流 '%s' 详情失败: %w", wf.ID, err)
		}
		payloads = append(payloads, *payload)
	}
	return &Snapshot{Workflows: payloads}, nil
}

// RestoreState 用快照替换全部工作流定义。
func (s *Service) RestoreState(ctx context.Context, snapshot *Snapshot) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启工作流快照事务失败: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err = tx.ExecContext(ctx, "DELETE FROM workflow_edges"); err != nil {
		return fmt.Errorf("清空工作流边表失败: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM workflow_nodes"); err != nil {
		return fmt.Errorf("清空工作流节点表失败: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM workflows"); err != nil {
		return fmt.Errorf("清空工作流表失败: %w", err)
	}

	const insertWorkflow = `INSERT INTO workflows (id, name, description, trigger_type, start_node_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	const insertNode = `INSERT INTO workflow_nodes (id, workflow_id, name, node_type, config_json) VALUES (?, ?, ?, ?, ?)`
	const insertEdgeWithID = `INSERT INTO workflow_edges (id, workflow_id, source_node_id, target_node_id, action, condition_json) VALUES (?, ?, ?, ?, ?, ?)`
	const insertEdge = `INSERT INTO workflow_edges (workflow_id, source_node_id, target_node_id, action, condition_json) VALUES (?, ?, ?, ?, ?)`

	for _, payload := range snapshot.Workflows {
		if _, err = tx.ExecContext(ctx, insertWorkflow, payload.Workflow.ID, payload.Workflow.Name, payload.Workflow.Description, payload.Workflow.TriggerType, payload.Workflow.StartNodeID); err != nil {
			return fmt.Errorf("恢复工作流 '%s' 失败: %w", payload.Workflow.ID, err)
		}
		for _, node := range payload.Nodes {
			node.WorkflowID = payload.Workflow.ID
			if _, err = tx.ExecContext(ctx, insertNode, node.ID, node.WorkflowID, node.Name, node.NodeType, node.Config); err != nil {
				return fmt.Errorf("恢复工作流 '%s' 的节点 '%s' 失败: %w", payload.Workflow.ID, node.ID, err)
			}
		}
		for _, edge := range payload.Edges {
			edge.WorkflowID = payload.Workflow.ID
			if edge.ID > 0 {
				if _, err = tx.ExecContext(ctx, insertEdgeWithID, edge.ID, edge.WorkflowID, edge.SourceNodeID, edge.TargetNodeID, edge.Action, edge.Condition); err != nil {
					return fmt.Errorf("恢复工作流 '%s' 的边 %d 失败: %w", payload.Workflow.ID, edge.ID, err)
				}
				continue
			}
			if _, err = tx.ExecContext(ctx, insertEdge, edge.WorkflowID, edge.SourceNodeID, edge.TargetNodeID, edge.Action, edge.Condition); err != nil {
				return fmt.Errorf("恢复工作流 '%s' 的边 '%s' -> '%s' 失败: %w", payload.Workflow.ID, edge.SourceNodeID, edge.TargetNodeID, err)
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交工作流快照事务失败: %w", err)
	}
	return nil
}
