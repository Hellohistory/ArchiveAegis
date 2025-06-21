// file: internal/adapter/datasource/grpc_client/client.go
package grpc_client

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"
)

// 编译期断言，确保 ClientAdapter 实现了 port.DataSource 接口
var _ port.DataSource = (*ClientAdapter)(nil)

// ClientAdapter 是一个适配器，它实现了port.DataSource接口，
// 但将其所有调用都转发给一个远程的gRPC插件。
type ClientAdapter struct {
	client datasourcev1.DataSourceClient
	conn   *grpc.ClientConn
}

// New 创建一个新的gRPC客户端适配器实例。
func New(pluginAddress string) (*ClientAdapter, error) {
	// 创建一个不安全的gRPC连接（本地开发用），未来可增加TLS
	conn, err := grpc.NewClient(pluginAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("无法连接到gRPC插件 at %s: %w", pluginAddress, err)
	}

	client := datasourcev1.NewDataSourceClient(conn)
	return &ClientAdapter{
		client: client,
		conn:   conn,
	}, nil
}

// GetPluginInfo 方法，用于调用插件的自我介绍接口
func (a *ClientAdapter) GetPluginInfo(ctx context.Context) (*datasourcev1.GetPluginInfoResponse, error) {
	slog.Debug("gRPC适配器: 正在向插件发送 GetPluginInfo 请求...")
	return a.client.GetPluginInfo(ctx, &datasourcev1.GetPluginInfoRequest{})
}

// Query 将通用的 Go map 转换为通用的 gRPC Struct
func (a *ClientAdapter) Query(ctx context.Context, req port.QueryRequest) (*port.QueryResult, error) {
	slog.Debug("gRPC适配器: 正在将 Query 请求转发到插件", "biz", req.BizName)

	// 将 Go 的 map[string]interface{} 转换为 gRPC 的 Struct
	queryStruct, err := structpb.NewStruct(req.Query)
	if err != nil {
		return nil, fmt.Errorf("创建 gRPC query struct 失败: %w", err)
	}

	grpcReq := &datasourcev1.QueryRequest{
		BizName: req.BizName,
		Query:   queryStruct,
	}

	// 发起RPC调用
	grpcRes, err := a.client.Query(ctx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC Query 调用失败: %w", err)
	}

	// 将 gRPC 的 Struct 响应转换为 Go 的 map[string]interface{}
	goResult := &port.QueryResult{
		Data:   grpcRes.GetData().AsMap(),
		Source: grpcRes.GetSource(),
	}

	return goResult, nil
}

// Mutate 方法现在也处理通用结构，代码大大简化
func (a *ClientAdapter) Mutate(ctx context.Context, req port.MutateRequest) (*port.MutateResult, error) {
	slog.Debug("gRPC适配器: 正在将 Mutate 请求转发到插件", "biz", req.BizName, "operation", req.Operation)

	// 将 Go 的 map[string]interface{} 转换为 gRPC 的 Struct
	payloadStruct, err := structpb.NewStruct(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("转换 Mutate payload 失败: %w", err)
	}

	grpcReq := &datasourcev1.MutateRequest{
		BizName:   req.BizName,
		Operation: req.Operation,
		Payload:   payloadStruct,
	}

	grpcRes, err := a.client.Mutate(ctx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC Mutate 调用失败: %w", err)
	}

	// 将 gRPC 的 Struct 响应转换为 Go 的 map[string]interface{}
	return &port.MutateResult{
		Data:   grpcRes.GetData().AsMap(),
		Source: grpcRes.GetSource(),
	}, nil
}

// GetSchema 方法的实现保持不变
func (a *ClientAdapter) GetSchema(ctx context.Context, req port.SchemaRequest) (*port.SchemaResult, error) {
	slog.Debug("gRPC适配器: 正在将 GetSchema 请求转发到插件", "biz", req.BizName)

	grpcReq := &datasourcev1.SchemaRequest{
		BizName:   req.BizName,
		TableName: req.TableName,
	}

	grpcRes, err := a.client.GetSchema(ctx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC GetSchema 调用失败: %w", err)
	}

	goTables := make(map[string][]port.FieldDescription)
	for tableName, tableSchema := range grpcRes.GetTables() {
		var goFields []port.FieldDescription
		for _, field := range tableSchema.GetFields() {
			goFields = append(goFields, port.FieldDescription{
				Name:         field.GetName(),
				DataType:     field.GetDataType(),
				IsSearchable: field.GetIsSearchable(),
				IsReturnable: field.GetIsReturnable(),
				IsPrimary:    field.GetIsPrimary(),
				Description:  field.GetDescription(),
			})
		}
		goTables[tableName] = goFields
	}

	return &port.SchemaResult{Tables: goTables}, nil
}

// HealthCheck 方法的实现保持不变
func (a *ClientAdapter) HealthCheck(ctx context.Context) error {
	slog.Debug("gRPC适配器: 正在将 HealthCheck 请求转发到插件...")

	res, err := a.client.HealthCheck(ctx, &datasourcev1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("gRPC HealthCheck 调用失败: %w", err)
	}

	if res.GetStatus() != datasourcev1.HealthCheckResponse_SERVING {
		return fmt.Errorf("插件报告不健康状态: %s", res.GetStatus().String())
	}

	return nil
}

// Close 关闭与gRPC插件的连接
func (a *ClientAdapter) Close() error {
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

// Type 返回适配器的类型标识符
func (a *ClientAdapter) Type() string {
	return "grpc_plugin"
}
