// Package domain 文件位置：internal/core/domain/workflow_models.go
package domain

import "encoding/json"

type Workflow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TriggerType string `json:"trigger_type"`
	StartNodeID string `json:"start_node_id"`
}

type WorkflowNode struct {
	ID         string          `json:"id"`
	WorkflowID string          `json:"workflow_id"`
	Name       string          `json:"name"`
	NodeType   string          `json:"node_type"` // e.g., "PLUGIN", "DATA_TRANSFORM"
	Config     json.RawMessage `json:"config"`    // 使用 RawMessage 延迟解析
}

type WorkflowEdge struct {
	ID           int64           `json:"id"`
	WorkflowID   string          `json:"workflow_id"`
	SourceNodeID string          `json:"source_node_id"`
	TargetNodeID string          `json:"target_node_id"`
	Action       string          `json:"action"`
	Condition    json.RawMessage `json:"condition,omitempty"`
}
