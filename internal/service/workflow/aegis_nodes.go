// Package workflow (aegis_nodes.go)
// 这个文件定义了各种具体的、可复用的 AegisNode 节点实现。
package workflow

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jmespath/go-jmespath"
)

// aegisNodeCore 是所有节点的通用核心实现，负责管理节点的ID和出边。
type aegisNodeCore struct {
	id    string
	edges []*AegisEdge
}

func (anc *aegisNodeCore) GetID() string {
	return anc.id
}

func (anc *aegisNodeCore) AddEdge(edge *AegisEdge) {
	if anc.edges == nil {
		anc.edges = make([]*AegisEdge, 0)
	}
	anc.edges = append(anc.edges, edge)
}

func (anc *aegisNodeCore) GetEdges() []*AegisEdge {
	return anc.edges
}

// --- AegisStandardNode ---

// AegisStandardNode 是一个具体的、支持三阶段生命周期和重试的节点实现。
type AegisStandardNode struct {
	aegisNodeCore
	MaxRetries       int
	WaitMilliseconds time.Duration
	PrepFunc         func(ctx context.Context, dataCtx *AegisDataContext) (any, error)
	ExecFunc         func(ctx context.Context, dataCtx *AegisDataContext, prepResult any) (any, error)
	PostFunc         func(ctx context.Context, dataCtx *AegisDataContext, execResult any) (string, error)
	ExecFallbackFunc func(ctx context.Context, dataCtx *AegisDataContext, prepResult any, lastErr error) (any, error)
}

// NewAegisStandardNode 创建一个带默认配置的标准节点。
func NewAegisStandardNode(id string) *AegisStandardNode {
	return &AegisStandardNode{
		aegisNodeCore:    aegisNodeCore{id: id},
		MaxRetries:       1,
		WaitMilliseconds: 0,
		PrepFunc:         func(ctx context.Context, dataCtx *AegisDataContext) (any, error) { return nil, nil },
		ExecFunc:         func(ctx context.Context, dataCtx *AegisDataContext, prepResult any) (any, error) { return nil, nil },
		PostFunc: func(ctx context.Context, dataCtx *AegisDataContext, execResult any) (string, error) {
			return "default", nil
		},
	}
}

func (asn *AegisStandardNode) Execute(ctx context.Context, dataCtx *AegisDataContext) (string, any, error) {
	prepRes, err := asn.PrepFunc(ctx, dataCtx)
	if err != nil {
		return "", nil, fmt.Errorf("prep阶段失败: %w", err)
	}

	var execRes any
	var lastExecErr error
	for i := 0; i < asn.MaxRetries; i++ {
		execRes, lastExecErr = asn.ExecFunc(ctx, dataCtx, prepRes)
		if lastExecErr == nil {
			break // 执行成功
		}
		if i < asn.MaxRetries-1 && asn.WaitMilliseconds > 0 {
			time.Sleep(asn.WaitMilliseconds * time.Millisecond)
		}
	}

	if lastExecErr != nil {
		if asn.ExecFallbackFunc != nil {
			execRes, err = asn.ExecFallbackFunc(ctx, dataCtx, prepRes, lastExecErr)
			if err != nil {
				return "", nil, fmt.Errorf("在 %d 次重试后, Fallback阶段失败: %w", asn.MaxRetries, err)
			}
		} else {
			return "", nil, fmt.Errorf("在 %d 次重试后, Exec阶段失败: %w", asn.MaxRetries, lastExecErr)
		}
	}

	action, err := asn.PostFunc(ctx, dataCtx, execRes)
	if err != nil {
		return "", nil, fmt.Errorf("post阶段失败: %w", err)
	}

	return action, execRes, nil
}

// --- AegisDataTransformNode ---

// AegisDataTransformNode 实现了一个使用JMESPath来转换数据的节点。
type AegisDataTransformNode struct {
	aegisNodeCore
	compiledMappings map[string]*jmespath.JMESPath
}

func (adtn *AegisDataTransformNode) Execute(ctx context.Context, dataCtx *AegisDataContext) (string, any, error) {
	output := make(map[string]any)
	inputData := map[string]any{
		"initial_params": dataCtx.InitialParams,
		"node_outputs":   dataCtx.NodeOutputs,
	}

	for key, compiledExpr := range adtn.compiledMappings {
		result, err := compiledExpr.Search(inputData)
		if err != nil {
			return "", nil, fmt.Errorf("对字段 '%s' 执行JMESPath表达式失败: %w", key, err)
		}
		output[key] = result
	}
	return "default", output, nil
}

// --- AegisParallelBatchNode ---

type indexedResult struct {
	index int
	value any
	err   error
}

// AegisParallelBatchNode 是一个并行处理数据切片的节点。
type AegisParallelBatchNode struct {
	AegisStandardNode
	ExecItemFunc func(ctx context.Context, dataCtx *AegisDataContext, item any) (any, error)
}

// NewAegisParallelBatchNode 创建一个新的并行批处理节点。
func NewAegisParallelBatchNode(id string) *AegisParallelBatchNode {
	n := &AegisParallelBatchNode{
		AegisStandardNode: *NewAegisStandardNode(id),
	}
	// 重写 ExecFunc 来实现并行处理逻辑
	n.ExecFunc = n.parallelExec
	return n
}

// parallelExec 是节点的核心执行逻辑。
func (apbn *AegisParallelBatchNode) parallelExec(ctx context.Context, dataCtx *AegisDataContext, prepResult any) (any, error) {
	items, ok := prepResult.([]any)
	if !ok {
		return nil, fmt.Errorf("prep阶段未返回 []any 类型，无法进行并行处理")
	}
	if len(items) == 0 {
		return []any{}, nil
	}
	if apbn.ExecItemFunc == nil {
		return nil, fmt.Errorf("并行批处理节点的 ExecItemFunc 未设置")
	}

	var wg sync.WaitGroup
	resultsChan := make(chan indexedResult, len(items))

	for i, item := range items {
		wg.Add(1)
		go func(idx int, it any) {
			defer wg.Done()
			res, err := apbn.ExecItemFunc(ctx, dataCtx, it)
			resultsChan <- indexedResult{index: idx, value: res, err: err}
		}(i, item)
	}

	wg.Wait()
	close(resultsChan)

	sortedResults := make([]any, len(items))
	for res := range resultsChan {
		if res.err != nil {
			return nil, fmt.Errorf("并行批处理项 %d 执行失败: %w", res.index, res.err)
		}
		sortedResults[res.index] = res.value
	}
	return sortedResults, nil
}
