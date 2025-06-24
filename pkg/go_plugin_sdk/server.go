// file: pkg/go_plugin_sdk/server.go
package go_plugin_sdk

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"

	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Serve 是 SDK 的主入口点。它处理所有服务启动的模板代码。
func Serve(info PluginInfo, initializer Initializer) error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true})))

	// 自动处理命令行参数
	portFlag := flag.Int("port", 50051, "服务监听端口")
	bizNameFlag := flag.String("biz", "", "此插件管理的业务组名称 (必须)")
	pluginNameFlag := flag.String("name", info.Name, "此插件实例的唯一名称")
	instanceDir := flag.String("instance_dir", "./instance", "实例目录的路径")
	flag.Parse()

	if *bizNameFlag == "" {
		return fmt.Errorf("启动失败：必须通过 -biz 参数指定插件管理的业务组名称")
	}

	cfg := PluginConfig{
		Port:        *portFlag,
		BizName:     *bizNameFlag,
		PluginName:  *pluginNameFlag,
		InstanceDir: *instanceDir,
	}

	slog.Info("🔌 启动插件服务...", "name", cfg.PluginName, "version", info.Version, "biz", cfg.BizName, "port", cfg.Port)

	// 调用开发者提供的初始化函数来获取业务逻辑实例
	ctx := context.Background()
	pluginLogic, err := initializer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("插件初始化失败: %w", err)
	}

	// 启动 gRPC 服务器
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("gRPC 服务监听端口失败: %w", err)
	}

	grpcServer := grpc.NewServer()

	// 创建一个内部的 gRPC 服务实现，它将请求路由到 pluginLogic
	s := &grpcPluginServer{
		logic:      pluginLogic,
		info:       info,
		pluginName: cfg.PluginName,
	}

	datasourcev1.RegisterDataSourceServer(grpcServer, s)

	slog.Info("✅ 插件启动成功，开始提供服务...")
	return grpcServer.Serve(lis)
}

// grpcPluginServer 是 datasourcev1.DataSourceServer 的内部实现。
type grpcPluginServer struct {
	datasourcev1.UnimplementedDataSourceServer
	logic      Plugin     // 开发者实现的业务逻辑
	info       PluginInfo // 插件元数据
	pluginName string     // 运行时确定的插件名称
}

// GetPluginInfo 自动根据 PluginInfo 和注册的处理器生成响应。
func (s *grpcPluginServer) GetPluginInfo(context.Context, *datasourcev1.GetPluginInfoRequest) (*datasourcev1.GetPluginInfoResponse, error) {
	return &datasourcev1.GetPluginInfoResponse{
		Name:                s.pluginName, // 使用从 flag 解析出的最终名称
		Version:             s.info.Version,
		Type:                s.info.Type,
		DescriptionMarkdown: s.info.DescriptionMarkdown,
		ContractVersion:     &datasourcev1.ApiVersion{Major: 1, Minor: 0, Patch: 0},
		SupportedPayloads: []string{ // 自动生成支持的载荷列表
			typeURL(&datasourcev1.DataQueryRequest{}),
			typeURL(&datasourcev1.DataMutateRequest{}),
			typeURL(&datasourcev1.GetSchemaRequest{}),
		},
		SupportedCapabilities: s.info.SupportedCapabilities,
	}, nil
}

// HealthCheck 直接代理到逻辑实现。
func (s *grpcPluginServer) HealthCheck(ctx context.Context, _ *datasourcev1.HealthCheckRequest) (*datasourcev1.HealthCheckResponse, error) {
	if err := s.logic.HealthCheck(ctx); err != nil {
		slog.Warn("插件健康检查失败", "error", err)
		return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_NOT_SERVING}, nil
	}
	return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_SERVING}, nil
}

// Execute 直接代理到逻辑实现。
func (s *grpcPluginServer) Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	return s.logic.Execute(ctx, req)
}

// typeURL 是一个辅助函数，用于获取 Protobuf 消息的类型 URL
func typeURL(m proto.Message) string {
	return "type.googleapis.com/" + string(m.ProtoReflect().Descriptor().FullName())
}
