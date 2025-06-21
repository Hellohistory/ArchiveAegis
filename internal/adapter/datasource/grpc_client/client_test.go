// file: internal/adapter/datasource/grpc_client/client_test.go

package grpc_client

import (
	"ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

// =======================================================================
// gRPC DataSourceClient Mock 实现，供适配器单元测试专用
// =======================================================================

type mockDataSourceClient struct {
	GetPluginInfoFunc func(ctx context.Context, req *datasourcev1.GetPluginInfoRequest, opts ...grpc.CallOption) (*datasourcev1.GetPluginInfoResponse, error)
	QueryFunc         func(ctx context.Context, req *datasourcev1.QueryRequest, opts ...grpc.CallOption) (*datasourcev1.QueryResult, error)
	MutateFunc        func(ctx context.Context, req *datasourcev1.MutateRequest, opts ...grpc.CallOption) (*datasourcev1.MutateResult, error)
	GetSchemaFunc     func(ctx context.Context, req *datasourcev1.SchemaRequest, opts ...grpc.CallOption) (*datasourcev1.SchemaResult, error)
	HealthCheckFunc   func(ctx context.Context, req *datasourcev1.HealthCheckRequest, opts ...grpc.CallOption) (*datasourcev1.HealthCheckResponse, error)
}

func (m *mockDataSourceClient) GetPluginInfo(ctx context.Context, req *datasourcev1.GetPluginInfoRequest, opts ...grpc.CallOption) (*datasourcev1.GetPluginInfoResponse, error) {
	return m.GetPluginInfoFunc(ctx, req, opts...)
}
func (m *mockDataSourceClient) Query(ctx context.Context, req *datasourcev1.QueryRequest, opts ...grpc.CallOption) (*datasourcev1.QueryResult, error) {
	return m.QueryFunc(ctx, req, opts...)
}
func (m *mockDataSourceClient) Mutate(ctx context.Context, req *datasourcev1.MutateRequest, opts ...grpc.CallOption) (*datasourcev1.MutateResult, error) {
	return m.MutateFunc(ctx, req, opts...)
}
func (m *mockDataSourceClient) GetSchema(ctx context.Context, req *datasourcev1.SchemaRequest, opts ...grpc.CallOption) (*datasourcev1.SchemaResult, error) {
	return m.GetSchemaFunc(ctx, req, opts...)
}
func (m *mockDataSourceClient) HealthCheck(ctx context.Context, req *datasourcev1.HealthCheckRequest, opts ...grpc.CallOption) (*datasourcev1.HealthCheckResponse, error) {
	return m.HealthCheckFunc(ctx, req, opts...)
}

// =======================================================================
// ClientAdapter 所有方法测试（包含异常分支）
// =======================================================================

func TestClientAdapter_AllMethods(t *testing.T) {
	ctx := context.Background()

	// 构造 mock 客户端
	mockClient := &mockDataSourceClient{}

	// 组装 ClientAdapter（conn 直接为 nil，不影响本地 mock 测试）
	adapter := &ClientAdapter{
		client: mockClient,
		conn:   nil,
	}

	// =============== GetPluginInfo 测试 ===============
	mockClient.GetPluginInfoFunc = func(ctx context.Context, req *datasourcev1.GetPluginInfoRequest, opts ...grpc.CallOption) (*datasourcev1.GetPluginInfoResponse, error) {
		return &datasourcev1.GetPluginInfoResponse{
			Name:    "TestPlugin",
			Version: "v1.0.0",
		}, nil
	}
	info, err := adapter.GetPluginInfo(ctx)
	if err != nil || info.Name != "TestPlugin" || info.Version != "v1.0.0" {
		t.Fatalf("GetPluginInfo 测试失败: %+v, err: %v", info, err)
	}

	// =============== Query 测试 ===============
	mockClient.QueryFunc = func(ctx context.Context, req *datasourcev1.QueryRequest, opts ...grpc.CallOption) (*datasourcev1.QueryResult, error) {
		// 构造 proto 的 Struct 列表
		s1, _ := structpb.NewStruct(map[string]interface{}{"id": 123, "name": "小明"})
		v1 := structpb.NewStructValue(s1)
		list := &structpb.ListValue{Values: []*structpb.Value{v1}}
		return &datasourcev1.QueryResult{
			Data:   list,
			Total:  1,
			Source: "test",
		}, nil
	}
	result, err := adapter.Query(ctx, port.QueryRequest{
		BizName:        "test_biz",
		TableName:      "user",
		QueryParams:    []port.QueryParam{{Field: "id", Value: "123"}},
		Page:           1,
		Size:           10,
		FieldsToReturn: []string{"id", "name"},
	})
	if err != nil {
		t.Fatalf("Query 测试失败: %v", err)
	}
	if len(result.Data) != 1 || result.Data[0]["id"] != float64(123) || result.Data[0]["name"] != "小明" {
		t.Fatalf("Query 数据转换失败: %+v", result.Data)
	}
	if result.Total != 1 || result.Source != "test" {
		t.Fatalf("Query 响应字段异常: %+v", result)
	}

	// =============== Mutate 测试 ===============
	mockClient.MutateFunc = func(ctx context.Context, req *datasourcev1.MutateRequest, opts ...grpc.CallOption) (*datasourcev1.MutateResult, error) {
		return &datasourcev1.MutateResult{Success: true, RowsAffected: 2, Message: "ok"}, nil
	}
	mutateRes, err := adapter.Mutate(ctx, port.MutateRequest{
		BizName:  "test_biz",
		CreateOp: &port.CreateOperation{TableName: "user", Data: map[string]interface{}{"name": "张三"}},
	})
	if err != nil || !mutateRes.Success || mutateRes.RowsAffected != 2 {
		t.Fatalf("Mutate 测试失败: %+v, err: %v", mutateRes, err)
	}

	// =============== GetSchema 测试 ===============
	mockClient.GetSchemaFunc = func(ctx context.Context, req *datasourcev1.SchemaRequest, opts ...grpc.CallOption) (*datasourcev1.SchemaResult, error) {
		return &datasourcev1.SchemaResult{
			Tables: map[string]*datasourcev1.TableSchema{
				"user": {
					Fields: []*datasourcev1.FieldDescription{
						{
							Name:         "id",
							DataType:     "int",
							IsPrimary:    true,
							IsSearchable: true,
							IsReturnable: true,
							Description:  "主键",
						},
					},
				},
			},
		}, nil
	}
	schema, err := adapter.GetSchema(ctx, port.SchemaRequest{BizName: "b", TableName: "user"})
	if err != nil {
		t.Fatalf("GetSchema 测试失败: %v", err)
	}
	if _, ok := schema.Tables["user"]; !ok {
		t.Fatalf("GetSchema 缺少 user 表")
	}
	if schema.Tables["user"][0].Name != "id" || !schema.Tables["user"][0].IsPrimary {
		t.Fatalf("GetSchema 字段解析异常: %+v", schema.Tables)
	}

	// =============== HealthCheck 测试（正常 SERVING） ===============
	mockClient.HealthCheckFunc = func(ctx context.Context, req *datasourcev1.HealthCheckRequest, opts ...grpc.CallOption) (*datasourcev1.HealthCheckResponse, error) {
		return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_SERVING}, nil
	}
	if err := adapter.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck 测试失败: %v", err)
	}

	// =============== HealthCheck 测试（非 SERVING） ===============
	mockClient.HealthCheckFunc = func(ctx context.Context, req *datasourcev1.HealthCheckRequest, opts ...grpc.CallOption) (*datasourcev1.HealthCheckResponse, error) {
		return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_UNKNOWN}, nil
	}
	if err := adapter.HealthCheck(ctx); err == nil {
		t.Fatalf("HealthCheck 非 SERVING 时应报错")
	}

	// =============== Close 测试（conn=nil，应该无异常） ===============
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close 方法应无异常: %v", err)
	}

	// =============== Type 方法测试 ===============
	if typ := adapter.Type(); typ != "grpc_plugin" {
		t.Fatalf("Type 方法返回值异常: %s", typ)
	}

	// =============== Query 错误分支 ===============
	mockClient.QueryFunc = func(ctx context.Context, req *datasourcev1.QueryRequest, opts ...grpc.CallOption) (*datasourcev1.QueryResult, error) {
		return nil, errors.New("fake query error")
	}
	if _, err := adapter.Query(ctx, port.QueryRequest{BizName: "b"}); err == nil {
		t.Fatalf("Query 错误分支未生效")
	}

	// =============== Mutate 错误分支 ===============
	mockClient.MutateFunc = func(ctx context.Context, req *datasourcev1.MutateRequest, opts ...grpc.CallOption) (*datasourcev1.MutateResult, error) {
		return nil, errors.New("fake mutate error")
	}
	if _, err := adapter.Mutate(ctx, port.MutateRequest{BizName: "b", DeleteOp: &port.DeleteOperation{}}); err == nil {
		t.Fatalf("Mutate 错误分支未生效")
	}

	// =============== GetSchema 错误分支 ===============
	mockClient.GetSchemaFunc = func(ctx context.Context, req *datasourcev1.SchemaRequest, opts ...grpc.CallOption) (*datasourcev1.SchemaResult, error) {
		return nil, errors.New("fake schema error")
	}
	if _, err := adapter.GetSchema(ctx, port.SchemaRequest{BizName: "b"}); err == nil {
		t.Fatalf("GetSchema 错误分支未生效")
	}

	// =============== HealthCheck 错误分支 ===============
	mockClient.HealthCheckFunc = func(ctx context.Context, req *datasourcev1.HealthCheckRequest, opts ...grpc.CallOption) (*datasourcev1.HealthCheckResponse, error) {
		return nil, errors.New("fake healthcheck error")
	}
	if err := adapter.HealthCheck(ctx); err == nil {
		t.Fatalf("HealthCheck 错误分支未生效")
	}
}
