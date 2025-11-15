// Package go_plugin_sdk 提供插件服务的通用运行框架
// 文件位置: pkg/go_plugin_sdk/server.go
package go_plugin_sdk

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Serve 启动插件服务，封装插件实例初始化与 gRPC 注册流程
func Serve(info PluginInfo, initializer Initializer) error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true})))

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pluginLogic, err := initializer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("插件初始化失败: %w", err)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("gRPC 服务监听端口失败: %w", err)
	}

	grpcServer := grpc.NewServer()

	apiVersion, err := parseProtocolVersion(info.Meta.SupportedProtocolVersion)
	if err != nil {
		return fmt.Errorf("插件声明的协议版本不合法: %w", err)
	}

	s := &grpcPluginServer{
		logic:      pluginLogic,
		info:       info,
		pluginName: cfg.PluginName,
		protocol:   apiVersion,
	}

	datasourcev1.RegisterDataSourceServer(grpcServer, s)

	go func() {
		slog.Info("✅ 插件启动成功，开始提供服务...")
		if err := grpcServer.Serve(lis); err != nil {
			slog.Info("gRPC 服务已停止", "error", err)
		}
	}()

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGINT, syscall.SIGTERM)
	<-stopChan

	slog.Info("收到关闭信号，开始执行优雅关闭...")

	grpcServer.GracefulStop()
	slog.Info("gRPC 服务已平滑停止。")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := pluginLogic.GracefulShutdown(shutdownCtx); err != nil {
		slog.Error("插件自定义清理逻辑执行失败", "error", err)
		return err
	}
	slog.Info("插件清理逻辑执行完毕。")
	slog.Info("👋 插件已成功优雅关闭。")
	return nil
}

// grpcPluginServer 是 gRPC 服务 datasourcev1.DataSourceServer 的实现
type grpcPluginServer struct {
	datasourcev1.UnimplementedDataSourceServer
	logic      Plugin     // 插件的业务逻辑实例
	info       PluginInfo // 插件元信息
	pluginName string     // 插件实例名称
	protocol   *datasourcev1.ApiVersion
}

// GetPluginInfo 返回插件的基本信息及支持能力
func (s *grpcPluginServer) GetPluginInfo(context.Context, *datasourcev1.GetPluginInfoRequest) (*datasourcev1.GetPluginInfoResponse, error) {
	return &datasourcev1.GetPluginInfoResponse{
		Name:                s.pluginName,
		Version:             s.info.Version,
		Type:                s.info.Type,
		DescriptionMarkdown: s.info.DescriptionMarkdown,
		ContractVersion:     s.protocol,
		SupportedPayloads: []string{
			typeURL(&datasourcev1.DataQueryRequest{}),
			typeURL(&datasourcev1.DataMutateRequest{}),
			typeURL(&datasourcev1.GetSchemaRequest{}),
		},
		SupportedCapabilities: s.info.SupportedCapabilities,
	}, nil
}

// HealthCheck 执行插件的健康检查逻辑
func (s *grpcPluginServer) HealthCheck(ctx context.Context, _ *datasourcev1.HealthCheckRequest) (*datasourcev1.HealthCheckResponse, error) {
	if err := s.logic.HealthCheck(ctx); err != nil {
		slog.Warn("插件健康检查失败", "error", err)
		return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_NOT_SERVING}, nil
	}
	return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_SERVING}, nil
}

// Execute 调用插件的通用执行逻辑处理请求
func (s *grpcPluginServer) Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	return s.logic.Execute(ctx, req)
}

// typeURL 返回 Protobuf 消息类型的 URL 表示
func typeURL(m proto.Message) string {
	return "type.googleapis.com/" + string(m.ProtoReflect().Descriptor().FullName())
}

func parseProtocolVersion(version string) (*datasourcev1.ApiVersion, error) {
	if strings.TrimSpace(version) == "" {
		// 默认保持 1.0.0 以兼容旧插件
		return &datasourcev1.ApiVersion{Major: 1, Minor: 0, Patch: 0}, nil
	}

	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("协议版本 '%s' 必须为 'major.minor.patch' 形式", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, errors.New("协议主版本必须为整数")
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, errors.New("协议次版本必须为整数")
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, errors.New("协议修订版本必须为整数")
	}

	return &datasourcev1.ApiVersion{Major: int32(major), Minor: int32(minor), Patch: int32(patch)}, nil
}
