// file: internal/adapter/datasource/grpc_client/client.go
package grpc_client

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"context"
	"fmt"
	"log"

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
// 函数签名已简化，不再需要 pluginType 参数。
func New(pluginAddress string) (*ClientAdapter, error) {
	// 创建一个不安全的gRPC连接（本地开发用），未来可增加TLS
	conn, err := grpc.NewClient(pluginAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("无法连接到gRPC插件 at %s: %w", pluginAddress, err)
	}

	client := datasourcev1.NewDataSourceClient(conn)
	// 连接成功日志移到网关的动态注册逻辑中，以便显示更丰富的信息
	return &ClientAdapter{
		client: client,
		conn:   conn,
	}, nil
}

// GetPluginInfo 方法，用于调用插件的自我介绍接口
func (a *ClientAdapter) GetPluginInfo(ctx context.Context) (*datasourcev1.GetPluginInfoResponse, error) {
	log.Printf("gRPC适配器: 正在向插件发送 GetPluginInfo 请求...")
	return a.client.GetPluginInfo(ctx, &datasourcev1.GetPluginInfoRequest{})
}

// Query 方法的实现保持不变
func (a *ClientAdapter) Query(ctx context.Context, req port.QueryRequest) (*port.QueryResult, error) {
	log.Printf("gRPC适配器: 正在将Query请求转发到插件 (biz: %s)...", req.BizName)

	// 将 Go 的 port.QueryRequest 转换为 gRPC 的 *datasourcev1.QueryRequest
	grpcParams := make([]*datasourcev1.QueryParam, len(req.QueryParams))
	for i, p := range req.QueryParams {
		grpcParams[i] = &datasourcev1.QueryParam{
			Field: p.Field,
			Value: p.Value,
			Logic: p.Logic,
			Fuzzy: p.Fuzzy,
		}
	}

	grpcReq := &datasourcev1.QueryRequest{
		BizName:        req.BizName,
		TableName:      req.TableName,
		QueryParams:    grpcParams,
		Page:           int32(req.Page),
		Size:           int32(req.Size),
		FieldsToReturn: req.FieldsToReturn,
	}

	// 发起RPC调用
	grpcRes, err := a.client.Query(ctx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC Query调用失败: %w", err)
	}

	// 将 gRPC 的 grpcRes (*datasourcev1.QueryResult) 转换为 Go 的 *port.QueryResult
	goData := make([]map[string]any, 0)
	if grpcRes.Data != nil {
		sliceData := grpcRes.Data.AsSlice()
		for _, item := range sliceData {
			if mapItem, ok := item.(map[string]any); ok {
				goData = append(goData, mapItem)
			}
		}
	}

	goResult := &port.QueryResult{
		Data:   goData,
		Total:  grpcRes.GetTotal(),
		Source: grpcRes.GetSource(),
	}

	return goResult, nil
}

// Mutate 方法的实现保持不变
func (a *ClientAdapter) Mutate(ctx context.Context, req port.MutateRequest) (*port.MutateResult, error) {
	log.Printf("gRPC适配器: 正在将Mutate请求转发到插件 (biz: %s)...", req.BizName)

	grpcReq := &datasourcev1.MutateRequest{BizName: req.BizName}
	switch {
	case req.CreateOp != nil:
		data, err := structpb.NewStruct(req.CreateOp.Data)
		if err != nil {
			return nil, fmt.Errorf("转换CreateOp数据失败: %w", err)
		}
		grpcReq.Operation = &datasourcev1.MutateRequest_CreateOp{
			CreateOp: &datasourcev1.CreateOperation{
				TableName: req.CreateOp.TableName,
				Data:      data,
			},
		}
	case req.UpdateOp != nil:
		data, err := structpb.NewStruct(req.UpdateOp.Data)
		if err != nil {
			return nil, fmt.Errorf("转换UpdateOp数据失败: %w", err)
		}
		filters := make([]*datasourcev1.QueryParam, len(req.UpdateOp.Filters))
		for i, f := range req.UpdateOp.Filters {
			filters[i] = &datasourcev1.QueryParam{Field: f.Field, Value: f.Value, Logic: f.Logic, Fuzzy: f.Fuzzy}
		}
		grpcReq.Operation = &datasourcev1.MutateRequest_UpdateOp{
			UpdateOp: &datasourcev1.UpdateOperation{
				TableName: req.UpdateOp.TableName,
				Data:      data,
				Filters:   filters,
			},
		}
	case req.DeleteOp != nil:
		filters := make([]*datasourcev1.QueryParam, len(req.DeleteOp.Filters))
		for i, f := range req.DeleteOp.Filters {
			filters[i] = &datasourcev1.QueryParam{Field: f.Field, Value: f.Value, Logic: f.Logic, Fuzzy: f.Fuzzy}
		}
		grpcReq.Operation = &datasourcev1.MutateRequest_DeleteOp{
			DeleteOp: &datasourcev1.DeleteOperation{
				TableName: req.DeleteOp.TableName,
				Filters:   filters,
			},
		}
	default:
		return nil, fmt.Errorf("无效的Mutate请求：缺少具体操作 (Create/Update/Delete)")
	}

	grpcRes, err := a.client.Mutate(ctx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC Mutate调用失败: %w", err)
	}

	return &port.MutateResult{
		Success:      grpcRes.GetSuccess(),
		RowsAffected: grpcRes.GetRowsAffected(),
		Message:      grpcRes.GetMessage(),
	}, nil
}

// GetSchema 方法的实现保持不变
func (a *ClientAdapter) GetSchema(ctx context.Context, req port.SchemaRequest) (*port.SchemaResult, error) {
	log.Printf("gRPC适配器: 正在将GetSchema请求转发到插件 (biz: %s)...", req.BizName)

	grpcReq := &datasourcev1.SchemaRequest{
		BizName:   req.BizName,
		TableName: req.TableName,
	}

	grpcRes, err := a.client.GetSchema(ctx, grpcReq)
	if err != nil {
		return nil, fmt.Errorf("gRPC GetSchema调用失败: %w", err)
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
	log.Printf("gRPC适配器: 正在将HealthCheck请求转发到插件...")

	res, err := a.client.HealthCheck(ctx, &datasourcev1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("gRPC HealthCheck调用失败: %w", err)
	}

	if res.GetStatus() != datasourcev1.HealthCheckResponse_SERVING {
		return fmt.Errorf("插件报告不健康状态: %s", res.GetStatus().String())
	}

	return nil
}

// Close 关闭与gRPC插件的连接
func (a *ClientAdapter) Close() error {
	return a.conn.Close()
}

// Type 返回适配器的类型标识符, 这里返回一个通用值，因为具体类型由插件决定
func (a *ClientAdapter) Type() string {
	return "grpc_plugin"
}
