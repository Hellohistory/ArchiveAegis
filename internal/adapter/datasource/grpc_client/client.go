// Package grpc_client provides a client adapter to communicate with remote gRPC
// data‑source plugins.
//
// 包 grpc_client 提供与远程 gRPC 数据源插件通信的客户端适配器，负责建立连接、发送请求并管理插件生命周期。
package grpc_client

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1" // gRPC generated DataSource service client interface
	"ArchiveAegis/internal/core/port"                      // Executor interface definition
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ClientAdapter implements port.Executor and proxies execution requests to a
// remote gRPC plugin.
//
// ClientAdapter 的职责：
//  1. 建立并复用到插件的 gRPC 连接。
//  2. 透明地转发 Execute / GetPluginInfo / HealthCheck 调用。
//  3. 在生命周期结束时正确释放连接资源。
//
// 所有导出方法均假设调用方通过 ctx 传递 deadline/取消信号，适配器本身不做重试策略。
// 如需 TLS/mTLS，请在调用 New 时替换证书选项。
//
// Example:
//
//	adapter, err := grpc_client.New("localhost:50051")
//	if err != nil { log.Fatal(err) }
//	defer adapter.Close()
//
//	res, err := adapter.Execute(ctx, req)
//	...
type ClientAdapter struct {
	client datasourcev1.DataSourceClient // underlying gRPC DataSource client
	conn   *grpc.ClientConn              // reusable gRPC connection
}

// _ 编译时断言确保实现了接口。
var _ port.Executor = (*ClientAdapter)(nil)

// New establishes an insecure gRPC connection to the plugin at pluginAddress
// and returns an initialised ClientAdapter.
//
// 调用方应在使用完毕后调用 Close 以释放连接资源。
func New(pluginAddress string) (*ClientAdapter, error) {
	conn, err := grpc.Dial(pluginAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc_client: dial %s: %w", pluginAddress, err)
	}

	return &ClientAdapter{
		client: datasourcev1.NewDataSourceClient(conn),
		conn:   conn,
	}, nil
}

// Execute forwards req to the remote plugin and returns its response.
//
// Execute 仅做透传，不做重试或超时控制；调用方可在 ctx 中设置 deadline。
func (a *ClientAdapter) Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	slog.Debug("grpc_client: Execute", "request_id", req.RequestId, "biz", req.BizName)
	return a.client.Execute(ctx, req)
}

// GetPluginInfo queries the plugin for its metadata such as version and
// supported capabilities.
//
// 用于启动阶段的能力验证。
func (a *ClientAdapter) GetPluginInfo(ctx context.Context) (*datasourcev1.GetPluginInfoResponse, error) {
	slog.Debug("grpc_client: GetPluginInfo")
	return a.client.GetPluginInfo(ctx, &datasourcev1.GetPluginInfoRequest{})
}

// HealthCheck returns an error if the plugin is not in SERVING state or the
// gRPC call fails.
//
// HealthCheck 便于上层探活或熔断。
func (a *ClientAdapter) HealthCheck(ctx context.Context) error {
	slog.Debug("grpc_client: HealthCheck")

	res, err := a.client.HealthCheck(ctx, &datasourcev1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("grpc_client: health check call: %w", err)
	}
	if res.GetStatus() != datasourcev1.HealthCheckResponse_SERVING {
		return fmt.Errorf("grpc_client: unhealthy plugin status: %s", res.GetStatus())
	}
	return nil
}

// Close closes the underlying gRPC connection. It is safe to call multiple
// times.
//
// 用于在适配器生命周期结束时释放网络资源。
func (a *ClientAdapter) Close() error {
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

// Type returns a constant identifier that distinguishes this executor among
// multiple implementations.
//
// 可用于插件注册表或日志输出中标识执行器类型。
func (a *ClientAdapter) Type() string {
	return "grpc_plugin"
}
