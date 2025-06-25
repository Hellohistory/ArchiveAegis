// Package grpc_client 提供 gRPC 数据源客户端适配器的实现
// 文件位置: internal/adapter/datasource/grpc_client/client.go
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

// 编译期校验：确保 ClientAdapter 实现 port.Executor 接口
var _ port.Executor = (*ClientAdapter)(nil)

// ClientAdapter 实现 port.Executor 接口，负责将请求转发给远程 gRPC 插件
type ClientAdapter struct {
	client datasourcev1.DataSourceClient // gRPC 客户端接口
	conn   *grpc.ClientConn              // gRPC 客户端连接
}

// New 创建并返回一个新的 gRPC 插件客户端适配器实例
func New(pluginAddress string) (*ClientAdapter, error) {
	conn, err := grpc.NewClient(pluginAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("无法连接到 gRPC 插件 %s: %w", pluginAddress, err)
	}

	client := datasourcev1.NewDataSourceClient(conn)
	return &ClientAdapter{
		client: client,
		conn:   conn,
	}, nil
}

// Execute 转发数据请求至 gRPC 插件端点
func (a *ClientAdapter) Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	slog.Debug("gRPC适配器: 正在将 Execute 请求转发到插件", "request_id", req.RequestId, "biz", req.BizName)
	return a.client.Execute(ctx, req)
}

// GetPluginInfo 获取插件信息
func (a *ClientAdapter) GetPluginInfo(ctx context.Context) (*datasourcev1.GetPluginInfoResponse, error) {
	slog.Debug("gRPC适配器: 正在向插件发送 GetPluginInfo 请求...")
	return a.client.GetPluginInfo(ctx, &datasourcev1.GetPluginInfoRequest{})
}

// HealthCheck 发送健康检查请求，判断插件是否处于正常运行状态
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

// Close 关闭 gRPC 客户端连接
func (a *ClientAdapter) Close() error {
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

// Type 返回客户端适配器类型标识符
func (a *ClientAdapter) Type() string {
	return "grpc_plugin"
}
