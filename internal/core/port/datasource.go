// Package port file: internal/core/port/datasource.go
package port

import (
	"ArchiveAegis/gen/go/proto/datasource/v1"
	"context"
	"errors"
)

// Standard errors
var (
	ErrPermissionDenied   = errors.New("权限不足，操作被拒绝")
	ErrBizNotFound        = errors.New("指定的业务组未找到")
	ErrTableNotFoundInBiz = errors.New("在当前业务组的配置中未找到指定的表")
)

type Executor interface {
	// Execute 是与插件交互的唯一入口。
	Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error)

	// HealthCheck 检查执行器的健康状况。
	HealthCheck(ctx context.Context) error

	// Type 返回适配器的类型标识符。
	Type() string
}

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

type SchemaRequest struct {
	BizName   string
	TableName string
}

// =============================================================================
//  标准化的业务模型 (与 Protobuf 定义对齐)
// =============================================================================

// FieldDescription 描述了一个字段的元数据
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
