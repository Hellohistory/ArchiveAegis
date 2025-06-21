// Package port file: internal/core/port/datasource.go
package port

import (
	"context"
	"errors"
)

// Standard errors
var (
	ErrPermissionDenied   = errors.New("权限不足，操作被拒绝")
	ErrBizNotFound        = errors.New("指定的业务组未找到")
	ErrTableNotFoundInBiz = errors.New("在当前业务组的配置中未找到指定的表")
)

type QueryRequest struct {
	BizName string
	Query   map[string]interface{}
}

type QueryResult struct {
	Data   map[string]interface{}
	Source string
}

type MutateRequest struct {
	BizName   string
	Operation string
	Payload   map[string]interface{}
}

type MutateResult struct {
	Data   map[string]interface{}
	Source string
}

// SchemaRequest 定义获取数据源结构信息的请求
type SchemaRequest struct {
	BizName   string
	TableName string
}

// FieldDescription 描述了一个字段的元数据 (V1版本)
type FieldDescription struct {
	Name         string `json:"name"`
	DataType     string `json:"data_type"`
	IsSearchable bool   `json:"is_searchable"`
	IsReturnable bool   `json:"is_returnable"`
	IsPrimary    bool   `json:"is_primary"`
	Description  string `json:"description"`
}

// SchemaResult 定义了数据源结构信息的返回
type SchemaResult struct {
	Tables map[string][]FieldDescription `json:"tables"`
}

// DataSource 接口定义
type DataSource interface {
	// Query 执行一次数据查询 (Read)
	Query(ctx context.Context, req QueryRequest) (*QueryResult, error)

	// Mutate 执行一次数据变更 (Create, Update, Delete)
	Mutate(ctx context.Context, req MutateRequest) (*MutateResult, error)

	// GetSchema 获取数据源的结构信息
	GetSchema(ctx context.Context, req SchemaRequest) (*SchemaResult, error)

	// HealthCheck 检查数据源的健康状况
	HealthCheck(ctx context.Context) error

	// Type 返回适配器的类型标识符
	Type() string
}
