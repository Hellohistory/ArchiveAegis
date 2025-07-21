// Package workflow (aegis_executor.go)
// 这个文件定义了Aegis工作流执行的核心接口、结构和运行时逻辑。
package workflow

import (
	"context"
	"fmt"
	"log"
)

// AegisDataContext 是在工作流实例执行期间，用于携带和管理所有数据的核心对象。
type AegisDataContext struct {
	// InitialParams 是工作流启动时的初始参数。只读。
	InitialParams map[string]any

	// NodeOutputs 存储了所有已执行节点的输出结果。
	// Key 是节点的数据库ID (string), Value 是该节点的输出 (any)。
	NodeOutputs map[string]any
}

// NewAegisDataContext 创建一个新的数据上下文。
func NewAegisDataContext(initialParams map[string]any) *AegisDataContext {
	if initialParams == nil {
		initialParams = make(map[string]any)
	}
	return &AegisDataContext{
		InitialParams: initialParams,
		NodeOutputs:   make(map[string]any),
	}
}

// AddOutput 用于在节点执行后，将其结果添加到上下文中。
func (adc *AegisDataContext) AddOutput(nodeID string, result any) {
	// nil 结果不存储，以避免覆盖或污染数据
	if result != nil {
		adc.NodeOutputs[nodeID] = result
	}
}

// AegisNode 是工作流中所有可执行单元的接口。
type AegisNode interface {
	// Execute 执行节点的逻辑。
	// 返回: action字符串, 节点的输出结果, 错误。
	Execute(ctx context.Context, dataCtx *AegisDataContext) (string, any, error)

	// AddEdge 添加一条从当前节点出发的边。
	AddEdge(edge *AegisEdge)

	// GetEdges 返回所有从当前节点出发的边。
	GetEdges() []*AegisEdge

	// GetID 返回节点的唯一标识符（通常是数据库ID）。
	GetID() string
}

// AegisConditionFunc 定义了边的触发条件。
// 它接收上一个节点的执行结果，返回是否应该沿着这条边走。
type AegisConditionFunc func(params map[string]any) (bool, error)

// AegisEdge 代表节点之间的有向连接。
type AegisEdge struct {
	TargetNode    AegisNode
	ConditionFunc AegisConditionFunc // 条件函数，为nil则为无条件边
	Action        string             // 仅在无条件时，用于匹配上个节点的action
}

// AegisWorkflowRunnable 代表一个可执行的工作流实例。
type AegisWorkflowRunnable struct {
	startNode AegisNode
	params    map[string]any
}

// NewAegisWorkflowRunnable 创建一个新的可执行工作流实例。
func NewAegisWorkflowRunnable(startNode AegisNode, initialParams map[string]any) *AegisWorkflowRunnable {
	return &AegisWorkflowRunnable{
		startNode: startNode,
		params:    initialParams,
	}
}

// Execute 启动工作流的执行。
func (awr *AegisWorkflowRunnable) Execute(ctx context.Context) (string, error) {
	currentNode := awr.startNode
	var lastAction string

	// 1. 在执行开始时，创建数据上下文
	dataCtx := NewAegisDataContext(awr.params)

	for currentNode != nil {
		var execResult any
		var execErr error

		log.Printf("信息: [AegisWorkflow] 正在执行节点 '%s'", currentNode.GetID())

		// 2. 将上下文传递给每个节点
		lastAction, execResult, execErr = currentNode.Execute(ctx, dataCtx)
		if execErr != nil {
			return "", fmt.Errorf("节点 '%s' 执行失败: %w", currentNode.GetID(), execErr)
		}

		log.Printf("信息: [AegisWorkflow] 节点 '%s' 执行完毕, action: '%s'", currentNode.GetID(), lastAction)

		// 3. 将节点的输出结果记录到上下文中
		dataCtx.AddOutput(currentNode.GetID(), execResult)

		// 4. 将结果传递给条件函数，以决定下一步
		nextNode := findNextAegisNode(currentNode, lastAction, execResult)
		currentNode = nextNode
	}

	log.Printf("信息: [AegisWorkflow] 流程执行完毕, 最终 action: '%s'", lastAction)
	return lastAction, nil
}

// findNextAegisNode 决定流程的下一步走向。
func findNextAegisNode(current AegisNode, action string, result any) AegisNode {
	// 准备用于条件评估的数据
	evalParams := map[string]any{
		"result": result,
	}

	// 优先检查有条件的边
	for _, edge := range current.GetEdges() {
		if edge.ConditionFunc != nil {
			match, err := edge.ConditionFunc(evalParams)
			if err != nil {
				log.Printf("警告: 评估节点 '%s' 的边条件时出错: %v", current.GetID(), err)
				continue // 出错则跳过此边
			}
			if match {
				log.Printf("信息: [AegisWorkflow] 节点 '%s' 条件匹配成功, 转向节点 '%s'", current.GetID(), edge.TargetNode.GetID())
				return edge.TargetNode
			}
		}
	}

	// 如果没有条件边满足，则寻找匹配 action 的无条件边
	if action == "" {
		action = "default"
	}
	for _, edge := range current.GetEdges() {
		if edge.ConditionFunc == nil && edge.Action == action {
			log.Printf("信息: [AegisWorkflow] 节点 '%s' action 匹配成功 ('%s'), 转向节点 '%s'", current.GetID(), action, edge.TargetNode.GetID())
			return edge.TargetNode
		}
	}

	log.Printf("信息: [AegisWorkflow] 节点 '%s' 没有找到匹配的后续节点。", current.GetID())
	return nil // 没有找到下一节点, 流程正常结束
}
