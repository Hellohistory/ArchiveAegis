// file: internal/adapter/datasource/grpc_client/client_test.go

package grpc_client

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/sharedmemory"
	"context"
	"net"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/anypb"
)

type mockDataSourceServer struct {
	datasourcev1.UnimplementedDataSourceServer
	healthy        bool
	sharedRows     []map[string]any
	lastHandlePath string
}

func (m *mockDataSourceServer) Execute(
	ctx context.Context,
	req *datasourcev1.RequestEnvelope,
) (*datasourcev1.ResponseEnvelope, error) {
	if len(m.sharedRows) > 0 {
		handle, err := sharedmemory.WriteJSONLines(m.sharedRows)
		if err != nil {
			return nil, err
		}
		m.lastHandlePath = handle.GetFilePath()
		packed, err := anypb.New(handle)
		if err != nil {
			return nil, err
		}
		return &datasourcev1.ResponseEnvelope{Payload: packed}, nil
	}

	return &datasourcev1.ResponseEnvelope{RequestId: req.GetRequestId()}, nil
}

func (m *mockDataSourceServer) GetPluginInfo(
	ctx context.Context,
	_ *datasourcev1.GetPluginInfoRequest,
) (*datasourcev1.GetPluginInfoResponse, error) {

	return &datasourcev1.GetPluginInfoResponse{
		Name:    "mock-plugin",
		Version: "v0.1.0",
	}, nil // ⚠️ 必须返回 (resp, nil)
}

func (m *mockDataSourceServer) HealthCheck(
	ctx context.Context,
	_ *datasourcev1.HealthCheckRequest,
) (*datasourcev1.HealthCheckResponse, error) {

	status := datasourcev1.HealthCheckResponse_SERVING
	if !m.healthy {
		status = datasourcev1.HealthCheckResponse_NOT_SERVING
	}
	return &datasourcev1.HealthCheckResponse{Status: status}, nil
}

// 启动本地 gRPC 服务
func startMockServer(tb testing.TB, healthy bool) (string, *grpc.Server) {
	tb.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("无法监听端口: %v", err)
	}

	srv := grpc.NewServer()
	datasourcev1.RegisterDataSourceServer(srv, &mockDataSourceServer{healthy: healthy})

	go func() {
		if serveErr := srv.Serve(lis); serveErr != nil {
			tb.Fatalf("gRPC 服务启动失败: %v", serveErr)
		}
	}()

	return lis.Addr().String(), srv
}

func TestClientAdapter_AllMethods(t *testing.T) {
	addr, srv := startMockServer(t, true)
	defer srv.Stop()

	adapter, err := New(addr)
	if err != nil {
		t.Fatalf("创建 ClientAdapter 失败: %v", err)
	}
	defer adapter.Close()

	ctx := context.Background()

	// Execute
	req := &datasourcev1.RequestEnvelope{RequestId: "req-001"}
	resp, err := adapter.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute 调用失败: %v", err)
	}
	if resp.GetRequestId() != req.GetRequestId() {
		t.Errorf("RequestId 不匹配: 得到 %s, 期望 %s", resp.GetRequestId(), req.GetRequestId())
	}

	// GetPluginInfo
	info, err := adapter.GetPluginInfo(ctx)
	if err != nil {
		t.Fatalf("GetPluginInfo 调用失败: %v", err)
	}
	if info.GetName() != "mock-plugin" || info.GetVersion() != "v0.1.0" {
		t.Errorf("GetPluginInfo 返回值异常: %+v", info)
	}

	if err := adapter.HealthCheck(ctx); err != nil {
		t.Errorf("HealthCheck 应成功，却返回错误: %v", err)
	}

	// Type
	if typ := adapter.Type(); typ != "grpc_plugin" {
		t.Errorf("Type() 返回错误: 得到 %s, 期望 %s", typ, "grpc_plugin")
	}
}

func TestClientAdapter_ExecuteSharedMemory(t *testing.T) {
	server := &mockDataSourceServer{healthy: true, sharedRows: []map[string]any{{"id": 1, "name": "bulk"}}}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法监听端口: %v", err)
	}
	grpcServer := grpc.NewServer()
	datasourcev1.RegisterDataSourceServer(grpcServer, server)
	go func() {
		if serveErr := grpcServer.Serve(lis); serveErr != nil {
			t.Fatalf("gRPC 服务启动失败: %v", serveErr)
		}
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		if server.lastHandlePath != "" {
			_ = os.Remove(server.lastHandlePath)
		}
	})

	adapter, err := New(lis.Addr().String())
	if err != nil {
		t.Fatalf("创建 ClientAdapter 失败: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	resp, err := adapter.Execute(context.Background(), &datasourcev1.RequestEnvelope{})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	result := &datasourcev1.DataQueryResult{}
	if err := resp.GetPayload().UnmarshalTo(result); err != nil {
		t.Fatalf("解包共享内存展开结果失败: %v", err)
	}
	if result.GetData().GetFields()["items"].GetListValue().GetValues()[0].GetStructValue().GetFields()["name"].GetStringValue() != "bulk" {
		t.Fatalf("展开后的数据内容不正确: %+v", result.GetData())
	}
}

func TestClientAdapter_HealthCheck_Failure(t *testing.T) {
	addr, srv := startMockServer(t, false)
	defer srv.Stop()

	adapter, err := New(addr)
	if err != nil {
		t.Fatalf("创建 ClientAdapter 失败: %v", err)
	}
	defer adapter.Close()

	if err := adapter.HealthCheck(context.Background()); err == nil {
		t.Error("预期 HealthCheck 返回错误，但得到 nil")
	}
}
