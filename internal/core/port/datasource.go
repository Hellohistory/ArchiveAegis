// Package port 定义数据源适配器的统一接口及相关模型
// 文件位置: internal/core/port/datasource.go
package port

import (
	"ArchiveAegis/gen/go/proto/datasource/v1"
	"context"
	"errors"
)

// 标准错误定义
var (
	ErrPermissionDenied   = errors.New("权限不足，操作被拒绝")
	ErrBizNotFound        = errors.New("指定的业务组未找到")
	ErrTableNotFoundInBiz = errors.New("在当前业务组的配置中未找到指定的表")
)

// Executor 定义了插件执行器的统一接口
type Executor interface {
	// Execute 是执行器的统一调用入口
	Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error)

	// HealthCheck 检查执行器的健康状态
	HealthCheck(ctx context.Context) error

	// Type 返回执行器的类型标识符
	Type() string
}

// QueryRequest 表示一次查询请求的参数
type QueryRequest struct {
	BizName string                 // 目标业务组名称
	Query   map[string]interface{} // 查询参数
}

// QueryResult 表示查询结果
type QueryResult struct {
	Data   map[string]interface{} // 查询返回数据
	Source string                 // 数据来源标识
}

// MutateRequest 表示一次写操作请求
type MutateRequest struct {
	BizName   string                 // 目标业务组名称
	Operation string                 // 操作类型（如 insert、update、delete）
	Payload   map[string]interface{} // 操作数据载荷
}

// MutateResult 表示写操作的返回结果
type MutateResult struct {
	Data   map[string]interface{} // 操作返回数据
	Source string                 // 数据来源标识
}

// SchemaRequest 表示结构查询请求参数
type SchemaRequest struct {
	BizName   string // 目标业务组名称
	TableName string // 目标表名称
}

// =============================================================================
// 标准化业务模型定义（与 Protobuf 保持对齐）
// =============================================================================

// FieldDescription 表示字段的元数据信息
type FieldDescription struct {
	Name         string `json:"name"`          // 字段名称
	DataType     string `json:"data_type"`     // 字段数据类型
	IsSearchable bool   `json:"is_searchable"` // 是否可作为查询条件
	IsReturnable bool   `json:"is_returnable"` // 是否在返回中出现
	IsPrimary    bool   `json:"is_primary"`    // 是否为主键字段
	Description  string `json:"description"`   // 字段描述
}

// SchemaResult 表示数据结构查询的结果
type SchemaResult struct {
	Tables map[string][]FieldDescription `json:"tables"` // 表结构映射
}
