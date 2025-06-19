// Package port file: internal/core/port/datasource.go
package port

import (
	"context"
	"errors"
)

// 将标准错误定义为接口契约的一部分
var (
	// ErrPermissionDenied 表示操作因权限不足而被拒绝。
	ErrPermissionDenied = errors.New("权限不足，操作被拒绝")

	// ErrBizNotFound 表示请求的业务组在系统中不存在或未被加载。
	ErrBizNotFound = errors.New("指定的业务组未找到")

	// ErrTableNotFoundInBiz 表示在给定的业务组配置中，未找到用户请求的表。
	ErrTableNotFoundInBiz = errors.New("在当前业务组的配置中未找到指定的表")
)

// QueryParam 定义了单个查询条件
type QueryParam struct {
	Field string `json:"field"`
	Value string `json:"value"`
	Logic string `json:"logic"`
	Fuzzy bool   `json:"fuzzy"`
}

// QueryRequest 定义了一个标准化的查询请求结构 (V1版本)
type QueryRequest struct {
	BizName        string       // 业务组的逻辑名称
	TableName      string       // 要查询的表名
	QueryParams    []QueryParam // 查询参数列表
	Page           int          // 页码 (1-based)
	Size           int          // 每页大小
	FieldsToReturn []string     // ✅ 客户端明确要求返回的字段列表
}

// QueryResult 定义了标准化的查询结果结构 (V1版本)
type QueryResult struct {
	Data   []map[string]any `json:"data"`
	Total  int64            `json:"total"`
	Source string           `json:"source"`
}

// MutateRequest 定义了一个标准的写操作（增、删、改）请求
type MutateRequest struct {
	BizName  string
	CreateOp *CreateOperation
	UpdateOp *UpdateOperation
	DeleteOp *DeleteOperation
}

// CreateOperation 定义了“创建”操作
type CreateOperation struct {
	TableName string                 `json:"table_name"`
	Data      map[string]interface{} `json:"data"`
}

// UpdateOperation 定义了“更新”操作
type UpdateOperation struct {
	TableName string                 `json:"table_name"`
	Data      map[string]interface{} `json:"data"`
	Filters   []QueryParam           `json:"filters"`
}

// DeleteOperation 定义了“删除”操作
type DeleteOperation struct {
	TableName string       `json:"table_name"`
	Filters   []QueryParam `json:"filters"`
}

// MutateResult 定义了写操作的标准返回结果
type MutateResult struct {
	Success      bool   `json:"success"`
	RowsAffected int64  `json:"rows_affected"`
	Message      string `json:"message"`
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

// DataSource 是所有数据源适配器都必须实现的接口 (V1版本)
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
