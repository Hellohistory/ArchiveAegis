// Package cluster 提供基于 Raft 的一致性与成员发现能力
// 文件位置: internal/cluster/handler.go

package cluster

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/service/plugin_manager"
	"ArchiveAegis/internal/service/workflow"
	"context"
	"encoding/json"
	"fmt"
)

// BusinessCommandHandler 将集群指令映射到具体的业务服务。
type BusinessCommandHandler struct {
	pluginManager   *plugin_manager.PluginManager
	workflowService *workflow.Service
}

// NewBusinessCommandHandler 构造一个默认的业务处理器。
func NewBusinessCommandHandler(pm *plugin_manager.PluginManager, ws *workflow.Service) *BusinessCommandHandler {
	return &BusinessCommandHandler{pluginManager: pm, workflowService: ws}
}

// HandleCommand 根据指令类型调用对应的服务方法。
func (h *BusinessCommandHandler) HandleCommand(cmd Command) (any, error) {
	switch cmd.Type {
	case CommandInstallPlugin:
		var payload PluginInstallPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析插件安装指令失败: %w", err)
		}
		return nil, h.pluginManager.Install(payload.PluginID, payload.Version)
	case CommandCreatePluginInstance:
		var payload PluginInstanceCreatePayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析实例创建指令失败: %w", err)
		}
		return h.pluginManager.CreateInstanceWithPlacement(payload.InstanceID, payload.Port, payload.DisplayName, payload.PluginID, payload.Version, payload.BizName)
	case CommandDeletePluginInstance:
		var payload PluginInstanceDeletePayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析实例删除指令失败: %w", err)
		}
		return nil, h.pluginManager.DeleteInstance(payload.InstanceID)
	case CommandCreateWorkflow:
		var payload workflow.FullWorkflowPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析工作流创建指令失败: %w", err)
		}
		result, err := h.workflowService.CreateWorkflow(context.Background(), payload.Workflow, payload.Nodes, payload.Edges)
		return result, err
	case CommandUpdateWorkflow:
		var payload workflow.FullWorkflowPayload
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析工作流更新指令失败: %w", err)
		}
		result, err := h.workflowService.UpdateWorkflow(context.Background(), payload.Workflow, payload.Nodes, payload.Edges)
		return result, err
	case CommandDeleteWorkflow:
		var payload domain.Workflow
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析工作流删除指令失败: %w", err)
		}
		return nil, h.workflowService.DeleteWorkflow(context.Background(), payload.ID)
	case CommandAddWorkflowNode:
		var payload WorkflowNodeEnvelope
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析节点新增指令失败: %w", err)
		}
		node := domain.WorkflowNode{}
		if err := decodeStruct(payload.Node, &node); err != nil {
			return nil, err
		}
		return h.workflowService.AddNode(context.Background(), payload.WorkflowID, node)
	case CommandUpdateWorkflowNode:
		var payload WorkflowNodeEnvelope
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析节点更新指令失败: %w", err)
		}
		node := domain.WorkflowNode{}
		if err := decodeStruct(payload.Node, &node); err != nil {
			return nil, err
		}
		return h.workflowService.UpdateNode(context.Background(), payload.WorkflowID, payload.NodeID, node)
	case CommandDeleteWorkflowNode:
		var payload WorkflowNodeEnvelope
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析节点删除指令失败: %w", err)
		}
		return nil, h.workflowService.DeleteNode(context.Background(), payload.WorkflowID, payload.NodeID)
	case CommandAddWorkflowEdge:
		var payload WorkflowEdgeEnvelope
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析边新增指令失败: %w", err)
		}
		edge := domain.WorkflowEdge{}
		if err := decodeStruct(payload.Edge, &edge); err != nil {
			return nil, err
		}
		return h.workflowService.AddEdge(context.Background(), payload.WorkflowID, edge)
	case CommandUpdateWorkflowEdge:
		var payload WorkflowEdgeEnvelope
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析边更新指令失败: %w", err)
		}
		edge := domain.WorkflowEdge{}
		if err := decodeStruct(payload.Edge, &edge); err != nil {
			return nil, err
		}
		return h.workflowService.UpdateEdge(context.Background(), payload.WorkflowID, payload.EdgeID, edge)
	case CommandDeleteWorkflowEdge:
		var payload WorkflowEdgeEnvelope
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, fmt.Errorf("解析边删除指令失败: %w", err)
		}
		return nil, h.workflowService.DeleteEdge(context.Background(), payload.WorkflowID, payload.EdgeID)
	default:
		return nil, fmt.Errorf("未知的指令类型: %s", cmd.Type)
	}
}

// SnapshotState 导出插件与工作流的完整状态。
func (h *BusinessCommandHandler) SnapshotState(ctx context.Context) (*StateSnapshot, error) {
	plugins, err := h.pluginManager.SnapshotState(ctx)
	if err != nil {
		return nil, err
	}
	flows, err := h.workflowService.SnapshotState(ctx)
	if err != nil {
		return nil, err
	}
	return &StateSnapshot{Plugins: plugins, Workflows: flows}, nil
}

// RestoreState 根据快照重建业务状态。
func (h *BusinessCommandHandler) RestoreState(ctx context.Context, snapshot *StateSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("快照数据为空")
	}
	if snapshot.Plugins != nil {
		if err := h.pluginManager.RestoreState(ctx, snapshot.Plugins); err != nil {
			return err
		}
	}
	if snapshot.Workflows != nil {
		if err := h.workflowService.RestoreState(ctx, snapshot.Workflows); err != nil {
			return err
		}
	}
	return nil
}

func decodeStruct(input interface{}, target interface{}) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("序列化临时对象失败: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("反序列化临时对象失败: %w", err)
	}
	return nil
}
