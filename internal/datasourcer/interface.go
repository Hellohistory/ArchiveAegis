// Package datasourcer internal/datasourcer/interface.go
package datasourcer

import "context"

// QueryRequest 定义了发起一次查询所需要的所有参数。
type QueryRequest struct {
	BizName        string
	TableName      string
	QueryParams    []Param
	FieldsToReturn []string
	Page           int
	Size           int
}

type Param struct {
	Field string
	Value string
	Fuzzy bool
	Logic string // 与下一个条件的逻辑关系
}

// QueryResult 定义了查询返回的数据。
type QueryResult []map[string]any

// Querier 是所有数据源（无论是本地SQLite还是远程gRPC插件）都必须实现的接口。
// 它只定义了一个核心能力：根据请求进行查询。
type Querier interface {
	Query(ctx context.Context, req QueryRequest) (QueryResult, error)
}
