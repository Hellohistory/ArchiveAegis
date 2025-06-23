// file: cmd/plugins/sqlite_plugin/main.go
package main

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/adapter/datasource/sqlite"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service/admin_config"
	"context"
	"database/sql"
	_ "embed"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

//go:embed README.md
var pluginDescription string

const pluginVersion = "1.0.0"

// server 结构体现在实现了新版 DataSourceServer 接口
// 其核心职责是代理请求到内部的 Executor
type server struct {
	datasourcev1.UnimplementedDataSourceServer
	manager    port.Executor
	pluginName string
	bizName    string
}

// GetPluginInfo 方法实现，现在返回更丰富的能力清单
func (s *server) GetPluginInfo(ctx context.Context, req *datasourcev1.GetPluginInfoRequest) (*datasourcev1.GetPluginInfoResponse, error) {
	slog.Info("插件收到 GetPluginInfo 请求")
	// 注意：这里的支持载荷列表应该与内置的 sqlite.Manager 支持的列表保持一致
	return &datasourcev1.GetPluginInfoResponse{
		Name:                s.pluginName,
		Version:             pluginVersion,
		Type:                "SQL",
		DescriptionMarkdown: pluginDescription,
		ContractVersion: &datasourcev1.ApiVersion{
			Major: 1,
			Minor: 0,
			Patch: 0,
		},
		SupportedPayloads: []string{
			_typeUrl(&datasourcev1.DataQueryRequest{}),
			_typeUrl(&datasourcev1.DataMutateRequest{}),
			_typeUrl(&datasourcev1.GetSchemaRequest{}),
		},
		SupportedCapabilities: []string{"AGGREGATION"},
	}, nil
}

// Execute 方法现在被极度简化，它直接将请求信封转发给内部的 manager。
func (s *server) Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	slog.Info("插件收到 Execute 请求，并直接转发给内置执行器", "request_id", req.RequestId, "biz", req.BizName, "payload_type", req.Payload.TypeUrl)

	return s.manager.Execute(ctx, req)
}

// HealthCheck 方法同样直接代理到 manager
func (s *server) HealthCheck(ctx context.Context, req *datasourcev1.HealthCheckRequest) (*datasourcev1.HealthCheckResponse, error) {
	err := s.manager.HealthCheck(ctx)
	if err != nil {
		slog.Warn("插件健康检查失败", "error", err)
		return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_NOT_SERVING}, nil
	}
	return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_SERVING}, nil
}

// _typeUrl 是一个辅助函数，用于获取 Protobuf 消息的类型 URL
func _typeUrl(m proto.Message) string {
	return "type.googleapis.com/" + string(m.ProtoReflect().Descriptor().FullName())
}

// main 函数将 sqlite.Manager (一个 Executor) 注入到 server 中
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true})))

	portFlag := flag.Int("port", 50051, "服务监听端口")
	bizNameFlag := flag.String("biz", "", "此插件管理的业务组名称 (必须)")
	pluginNameFlag := flag.String("name", "unnamed-sqlite-plugin", "此插件实例的唯一名称")
	instanceDir := flag.String("instance_dir", "./instance", "实例目录的路径")
	flag.Parse()

	if *bizNameFlag == "" {
		slog.Error("启动失败：必须通过 -biz 参数指定插件管理的业务组名称")
		os.Exit(1)
	}
	slog.Info("🔌 插件启动中...", "name", *pluginNameFlag, "version", pluginVersion, "biz", *bizNameFlag, "port", *portFlag)

	// --- 依赖初始化部分 ---
	slog.Info("正在初始化依赖...")
	authDbPath := filepath.Join(*instanceDir, "auth.db")
	pluginSysDB, err := initAuthDB(authDbPath)
	if err != nil {
		slog.Error("插件无法初始化认证数据库连接", "error", err)
		os.Exit(1)
	}
	defer pluginSysDB.Close()
	slog.Info("成功连接到 auth.db")

	adminConfigService, err := admin_config.NewAdminConfigServiceImpl(pluginSysDB, 100, 1*time.Minute)
	if err != nil {
		slog.Error("插件无法创建 AdminConfigService", "error", err)
		os.Exit(1)
	}
	slog.Info("成功创建 AdminConfigService")

	// sqlite.NewManager 返回一个实现了 port.Executor 接口的实例
	sqliteManager := sqlite.NewManager(adminConfigService)
	if err := sqliteManager.InitForBiz(context.Background(), *instanceDir, *bizNameFlag); err != nil {
		slog.Error("插件初始化业务失败", "biz", *bizNameFlag, "error", err)
		os.Exit(1)
	}
	slog.Info("成功初始化业务数据", "biz", *bizNameFlag)

	// --- gRPC 服务启动部分 ---
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *portFlag))
	if err != nil {
		slog.Error("gRPC 服务监听端口失败", "port", *portFlag, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	// 将实现了 Executor 接口的 sqliteManager 注入 server
	datasourcev1.RegisterDataSourceServer(grpcServer, &server{
		manager:    sqliteManager,
		pluginName: *pluginNameFlag,
		bizName:    *bizNameFlag,
	})

	slog.Info("✅ SQLite插件启动成功，开始提供服务...")
	if err := grpcServer.Serve(lis); err != nil {
		slog.Error("gRPC 服务启动失败", "error", err)
		os.Exit(1)
	}
}

func initAuthDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=ON&_synchronous=NORMAL", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开/创建认证数据库 '%s' 失败: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接认证数据库 '%s' (Ping) 失败: %w", path, err)
	}
	return db, nil
}
