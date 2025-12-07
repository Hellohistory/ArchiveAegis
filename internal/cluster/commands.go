// Package cluster 提供基于 Raft 的一致性与成员发现能力
// 文件位置: internal/cluster/commands.go

package cluster

// Command 表示写入 Raft 日志的业务指令。
// Type 用于区分业务域，Payload 为 JSON 编码的指令数据。
//
// 为了降低耦合度，该结构保持通用，具体解析逻辑由 CommandHandler 决定。
type Command struct {
	Type    string `json:"type"`
	Payload []byte `json:"payload"`
}

// 业务层使用的指令类型常量。
const (
	CommandInstallPlugin        = "plugin.install"
	CommandCreatePluginInstance = "plugin.instance.create"
	CommandDeletePluginInstance = "plugin.instance.delete"
	CommandCreateWorkflow       = "workflow.create"
	CommandUpdateWorkflow       = "workflow.update"
	CommandDeleteWorkflow       = "workflow.delete"
	CommandAddWorkflowNode      = "workflow.node.add"
	CommandUpdateWorkflowNode   = "workflow.node.update"
	CommandDeleteWorkflowNode   = "workflow.node.delete"
	CommandAddWorkflowEdge      = "workflow.edge.add"
	CommandUpdateWorkflowEdge   = "workflow.edge.update"
	CommandDeleteWorkflowEdge   = "workflow.edge.delete"
)

// PluginInstallPayload 描述插件安装操作的参数。
type PluginInstallPayload struct {
	PluginID string `json:"plugin_id"`
	Version  string `json:"version"`
}

// PluginInstanceCreatePayload 描述插件实例创建的参数。
// InstanceID 与 Port 由领导者生成并在复制时保持一致。
type PluginInstanceCreatePayload struct {
	InstanceID  string `json:"instance_id"`
	Port        int    `json:"port"`
	DisplayName string `json:"display_name"`
	PluginID    string `json:"plugin_id"`
	Version     string `json:"version"`
	BizName     string `json:"biz_name"`
}

// PluginInstanceDeletePayload 描述插件实例删除指令。
type PluginInstanceDeletePayload struct {
	InstanceID string `json:"instance_id"`
}

// WorkflowEnvelope 用于携带完整的工作流定义。
type WorkflowEnvelope struct {
	Workflow any `json:"workflow"`
	Nodes    any `json:"nodes"`
	Edges    any `json:"edges"`
}

// WorkflowNodeEnvelope 携带节点级操作的上下文。
type WorkflowNodeEnvelope struct {
	WorkflowID string      `json:"workflow_id"`
	NodeID     string      `json:"node_id,omitempty"`
	Node       interface{} `json:"node"`
}

// WorkflowEdgeEnvelope 携带边级操作的上下文。
type WorkflowEdgeEnvelope struct {
	WorkflowID string      `json:"workflow_id"`
	EdgeID     int64       `json:"edge_id,omitempty"`
	Edge       interface{} `json:"edge"`
}
