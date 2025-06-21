// file: internal/adapter/datasource/grpc_client/client_test.go

package grpc_client

import (
	"ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"context"
	"errors"
	"reflect"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

// =======================================================================
// gRPC DataSourceClient Mock 实现，供适配器单元测试专用
// =======================================================================

// mockDataSourceClient 是 datasourcev1.DataSourceClient 接口的一个 mock 实现
type mockDataSourceClient struct {
	GetPluginInfoFunc func(ctx context.Context, req *datasourcev1.GetPluginInfoRequest, opts ...grpc.CallOption) (*datasourcev1.GetPluginInfoResponse, error)
	// --- 修正点: 将 QueryResponse 修改回 QueryResult ---
	QueryFunc func(ctx context.Context, req *datasourcev1.QueryRequest, opts ...grpc.CallOption) (*datasourcev1.QueryResult, error)
	// --- 修正点: 将 MutateResponse 修改回 MutateResult ---
	MutateFunc func(ctx context.Context, req *datasourcev1.MutateRequest, opts ...grpc.CallOption) (*datasourcev1.MutateResult, error)
	// --- 修正点: 将 SchemaResponse 修改回 SchemaResult ---
	GetSchemaFunc   func(ctx context.Context, req *datasourcev1.SchemaRequest, opts ...grpc.CallOption) (*datasourcev1.SchemaResult, error)
	HealthCheckFunc func(ctx context.Context, req *datasourcev1.HealthCheckRequest, opts ...grpc.CallOption) (*datasourcev1.HealthCheckResponse, error)
}

// 以下是 mockDataSourceClient 对接口的实现
func (m *mockDataSourceClient) GetPluginInfo(ctx context.Context, req *datasourcev1.GetPluginInfoRequest, opts ...grpc.CallOption) (*datasourcev1.GetPluginInfoResponse, error) {
	return m.GetPluginInfoFunc(ctx, req, opts...)
}

// --- 修正点: 将 QueryResponse 修改回 QueryResult ---
func (m *mockDataSourceClient) Query(ctx context.Context, req *datasourcev1.QueryRequest, opts ...grpc.CallOption) (*datasourcev1.QueryResult, error) {
	return m.QueryFunc(ctx, req, opts...)
}

// --- 修正点: 将 MutateResponse 修改回 MutateResult ---
func (m *mockDataSourceClient) Mutate(ctx context.Context, req *datasourcev1.MutateRequest, opts ...grpc.CallOption) (*datasourcev1.MutateResult, error) {
	return m.MutateFunc(ctx, req, opts...)
}

// --- 修正点: 将 SchemaResponse 修改回 SchemaResult ---
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

	mockClient := &mockDataSourceClient{}
	adapter := &ClientAdapter{
		client: mockClient,
		conn:   nil, // conn 在 mock 测试中不重要
	}

	t.Run("GetPluginInfo_Success", func(t *testing.T) {
		mockClient.GetPluginInfoFunc = func(ctx context.Context, req *datasourcev1.GetPluginInfoRequest, opts ...grpc.CallOption) (*datasourcev1.GetPluginInfoResponse, error) {
			return &datasourcev1.GetPluginInfoResponse{Name: "TestPlugin", Version: "v1.0.0"}, nil
		}
		info, err := adapter.GetPluginInfo(ctx)
		if err != nil || info.Name != "TestPlugin" || info.Version != "v1.0.0" {
			t.Errorf("GetPluginInfo 测试失败: got %+v, err: %v", info, err)
		}
	})

	t.Run("Query_Success", func(t *testing.T) {
		mockResponseData := map[string]interface{}{"id": float64(123), "name": "小明"}
		mockResponseStruct, _ := structpb.NewStruct(mockResponseData)

		// --- 修正点: 将 QueryResponse 修改回 QueryResult ---
		mockClient.QueryFunc = func(ctx context.Context, req *datasourcev1.QueryRequest, opts ...grpc.CallOption) (*datasourcev1.QueryResult, error) {
			if req.GetBizName() != "user_biz" {
				t.Errorf("Query 请求 BizName 不匹配: got %s", req.GetBizName())
			}
			if req.Query.AsMap()["id"] != float64(1) {
				t.Errorf("Query 请求参数不匹配: got %v", req.Query.AsMap())
			}
			return &datasourcev1.QueryResult{
				Data:   mockResponseStruct,
				Source: "mock_plugin_query",
			}, nil
		}

		result, err := adapter.Query(ctx, port.QueryRequest{
			BizName: "user_biz",
			Query:   map[string]interface{}{"id": 1},
		})

		if err != nil {
			t.Errorf("Query 测试不应报错: %v", err)
		}
		if !reflect.DeepEqual(result.Data, mockResponseData) {
			t.Errorf("Query 响应数据转换失败: got %+v, want %+v", result.Data, mockResponseData)
		}
		if result.Source != "mock_plugin_query" {
			t.Errorf("Query 响应 Source 异常: got %s", result.Source)
		}
	})

	t.Run("Mutate_Success", func(t *testing.T) {
		mockResponseData := map[string]interface{}{"id": float64(456), "status": "created"}
		mockResponseStruct, _ := structpb.NewStruct(mockResponseData)

		// --- 修正点: 将 MutateResponse 修改回 MutateResult ---
		mockClient.MutateFunc = func(ctx context.Context, req *datasourcev1.MutateRequest, opts ...grpc.CallOption) (*datasourcev1.MutateResult, error) {
			if req.GetOperation() != "CREATE" {
				t.Errorf("Mutate 请求 Operation 不匹配: got %s", req.GetOperation())
			}
			return &datasourcev1.MutateResult{
				Data:   mockResponseStruct,
				Source: "mock_plugin_mutate",
			}, nil
		}

		result, err := adapter.Mutate(ctx, port.MutateRequest{
			BizName:   "user_biz",
			Operation: "CREATE",
			Payload:   map[string]interface{}{"name": "李华"},
		})

		if err != nil {
			t.Errorf("Mutate 测试不应报错: %v", err)
		}
		if !reflect.DeepEqual(result.Data, mockResponseData) {
			t.Errorf("Mutate 响应数据转换失败: got %+v, want %+v", result.Data, mockResponseData)
		}
		if result.Source != "mock_plugin_mutate" {
			t.Errorf("Mutate 响应 Source 异常: got %s", result.Source)
		}
	})

	t.Run("GetSchema_Success", func(t *testing.T) {
		// --- 修正点: 将 SchemaResponse 修改回 SchemaResult ---
		mockClient.GetSchemaFunc = func(ctx context.Context, req *datasourcev1.SchemaRequest, opts ...grpc.CallOption) (*datasourcev1.SchemaResult, error) {
			return &datasourcev1.SchemaResult{
				Tables: map[string]*datasourcev1.TableSchema{
					"user": {Fields: []*datasourcev1.FieldDescription{{Name: "id", IsPrimary: true}}},
				},
			}, nil
		}
		schema, err := adapter.GetSchema(ctx, port.SchemaRequest{BizName: "b", TableName: "user"})
		if err != nil {
			t.Errorf("GetSchema 测试失败: %v", err)
		}
		if _, ok := schema.Tables["user"]; !ok || schema.Tables["user"][0].Name != "id" {
			t.Errorf("GetSchema 字段解析异常: %+v", schema.Tables)
		}
	})

	t.Run("HealthCheck_SuccessAndFailure", func(t *testing.T) {
		mockClient.HealthCheckFunc = func(ctx context.Context, req *datasourcev1.HealthCheckRequest, opts ...grpc.CallOption) (*datasourcev1.HealthCheckResponse, error) {
			return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_SERVING}, nil
		}
		if err := adapter.HealthCheck(ctx); err != nil {
			t.Errorf("HealthCheck (SERVING) 测试失败: %v", err)
		}

		mockClient.HealthCheckFunc = func(ctx context.Context, req *datasourcev1.HealthCheckRequest, opts ...grpc.CallOption) (*datasourcev1.HealthCheckResponse, error) {
			return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_UNKNOWN}, nil
		}
		if err := adapter.HealthCheck(ctx); err == nil {
			t.Error("HealthCheck (非 SERVING) 时应报错")
		}
	})

	t.Run("CloseAndType", func(t *testing.T) {
		if err := adapter.Close(); err != nil {
			t.Errorf("Close 方法应无异常: %v", err)
		}
		if typ := adapter.Type(); typ != "grpc_plugin" {
			t.Errorf("Type 方法返回值异常: got %s", typ)
		}
	})

	t.Run("ErrorHandling", func(t *testing.T) {
		mockClient.QueryFunc = func(ctx context.Context, req *datasourcev1.QueryRequest, opts ...grpc.CallOption) (*datasourcev1.QueryResult, error) {
			return nil, errors.New("fake query rpc error")
		}
		if _, err := adapter.Query(ctx, port.QueryRequest{}); err == nil {
			t.Error("Query gRPC 错误分支未生效")
		}

		mockClient.MutateFunc = func(ctx context.Context, req *datasourcev1.MutateRequest, opts ...grpc.CallOption) (*datasourcev1.MutateResult, error) {
			return nil, errors.New("fake mutate rpc error")
		}
		if _, err := adapter.Mutate(ctx, port.MutateRequest{}); err == nil {
			t.Error("Mutate gRPC 错误分支未生效")
		}

		unserializableQuery := port.QueryRequest{Query: map[string]interface{}{"unsupported": func() {}}}
		if _, err := adapter.Query(ctx, unserializableQuery); err == nil {
			t.Error("Query structpb 转换错误分支未生效")
		}

		unserializableMutate := port.MutateRequest{Payload: map[string]interface{}{"unsupported": func() {}}}
		if _, err := adapter.Mutate(ctx, unserializableMutate); err == nil {
			t.Error("Mutate structpb 转换错误分支未生效")
		}
	})
}
