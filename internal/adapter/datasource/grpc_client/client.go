// Package grpc_client file: internal/adapter/datasource/grpc_client/client.go
package grpc_client

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// 编译期断言，确保 ClientAdapter 实现了新的 port.Executor 接口
var _ port.Executor = (*ClientAdapter)(nil)

// ClientAdapter 是一个适配器，它实现了 port.Executor 接口，
// 将其所有调用都转发给一个远程的 gRPC 插件。
type ClientAdapter struct {
	client datasourcev1.DataSourceClient
	conn   *grpc.ClientConn
}

// New 创建一个gRPC客户端适配器实例。
func New(pluginAddress string) (*ClientAdapter, error) {
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

// Execute 直接将请求信封转发给插件。
func (a *ClientAdapter) Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	slog.Debug("gRPC适配器: 正在将 Execute 请求转发到插件", "request_id", req.RequestId, "biz", req.BizName)
	return a.client.Execute(ctx, req)
}

// GetPluginInfo 用于插件发现。
func (a *ClientAdapter) GetPluginInfo(ctx context.Context) (*datasourcev1.GetPluginInfoResponse, error) {
	slog.Debug("gRPC适配器: 正在向插件发送 GetPluginInfo 请求...")
	return a.client.GetPluginInfo(ctx, &datasourcev1.GetPluginInfoRequest{})
}

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

// Close 关闭与gRPC插件的连接。
func (a *ClientAdapter) Close() error {
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

// Type 返回适配器的类型标识符。
func (a *ClientAdapter) Type() string {
	return "grpc_plugin"
}
