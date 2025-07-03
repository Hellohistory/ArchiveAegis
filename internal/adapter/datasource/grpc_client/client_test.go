// file: internal/adapter/datasource/grpc_client/client_test.go

package grpc_client

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
)

type mockDataSourceServer struct {
	datasourcev1.UnimplementedDataSourceServer
	healthy bool
}

func (m *mockDataSourceServer) Execute(
	ctx context.Context,
	req *datasourcev1.RequestEnvelope,
) (*datasourcev1.ResponseEnvelope, error) {

	return &datasourcev1.ResponseEnvelope{
		RequestId: req.GetRequestId(),
	}, nil
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
