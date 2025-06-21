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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	_ "modernc.org/sqlite"
)

//go:embed README.md
var pluginDescription string

const pluginVersion = "1.0.0"

// server 结构体实现了 gRPC 生成的 DataSourceServer 接口
type server struct {
	datasourcev1.UnimplementedDataSourceServer
	manager    port.DataSource
	pluginName string
	bizName    string
}

// GetPluginInfo 方法实现
func (s *server) GetPluginInfo(ctx context.Context, req *datasourcev1.GetPluginInfoRequest) (*datasourcev1.GetPluginInfoResponse, error) {
	slog.Info("插件收到 GetPluginInfo 请求")
	return &datasourcev1.GetPluginInfoResponse{
		Name:                s.pluginName,
		Version:             pluginVersion,
		Type:                "sqlite_plugin",
		SupportedBizNames:   []string{s.bizName},
		DescriptionMarkdown: pluginDescription,
	}, nil
}

// Query 方法现在处理通用的 gRPC 请求
func (s *server) Query(ctx context.Context, req *datasourcev1.QueryRequest) (*datasourcev1.QueryResult, error) {
	queryStruct := req.GetQuery()
	if queryStruct == nil {
		return nil, status.Error(codes.InvalidArgument, "查询体 (query) 不能为空")
	}

	// 直接将收到的通用查询对象传递给核心 port.QueryRequest
	goReq := port.QueryRequest{
		BizName: req.BizName,
		Query:   queryStruct.AsMap(),
	}

	slog.Info("插件收到 Query 请求", "biz", req.BizName)
	result, err := s.manager.Query(ctx, goReq)
	if err != nil {
		slog.Error("插件执行 Query 失败", "error", err)
		return nil, status.Errorf(codes.Internal, "查询数据失败: %v", err)
	}

	// 将 manager 返回的通用 map 结果包装成 gRPC 的 Struct
	resultData, err := structpb.NewStruct(result.Data)
	if err != nil {
		slog.Error("转换查询结果为 structpb.Struct 失败", "error", err)
		return nil, status.Errorf(codes.Internal, "序列化查询结果失败: %v", err)
	}

	return &datasourcev1.QueryResult{
		Data:   resultData,
		Source: result.Source,
	}, nil
}

// Mutate 方法现在处理通用的 gRPC 请求
func (s *server) Mutate(ctx context.Context, req *datasourcev1.MutateRequest) (*datasourcev1.MutateResult, error) {
	slog.Info("插件收到 Mutate 请求", "biz", req.BizName, "operation", req.Operation)

	// 直接将收到的通用载荷对象传递给核心 port.MutateRequest
	goReq := port.MutateRequest{
		BizName:   req.BizName,
		Operation: req.Operation,
		Payload:   req.GetPayload().AsMap(),
	}

	goResult, err := s.manager.Mutate(ctx, goReq)
	if err != nil {
		slog.Error("插件执行 Mutate 失败", "error", err)
		return nil, status.Errorf(codes.Internal, "写操作失败: %v", err)
	}

	// 将 manager 返回的通用 map 结果包装成 gRPC 的 Struct
	resultData, err := structpb.NewStruct(goResult.Data)
	if err != nil {
		slog.Error("转换 Mutate 结果为 structpb.Struct 失败", "error", err)
		return nil, status.Errorf(codes.Internal, "序列化写操作结果失败: %v", err)
	}

	return &datasourcev1.MutateResult{
		Data:   resultData,
		Source: goResult.Source,
	}, nil
}

func (s *server) GetSchema(ctx context.Context, req *datasourcev1.SchemaRequest) (*datasourcev1.SchemaResult, error) {
	slog.Info("插件收到 GetSchema 请求", "biz", req.BizName)
	goReq := port.SchemaRequest{BizName: req.BizName, TableName: req.TableName}

	result, err := s.manager.GetSchema(ctx, goReq)
	if err != nil {
		return nil, err
	}

	grpcTables := make(map[string]*datasourcev1.TableSchema)
	for tableName, tableSchema := range result.Tables {
		var grpcFields []*datasourcev1.FieldDescription
		for _, field := range tableSchema {
			grpcFields = append(grpcFields, &datasourcev1.FieldDescription{
				Name:         field.Name,
				DataType:     field.DataType,
				IsSearchable: field.IsSearchable,
				IsReturnable: field.IsReturnable,
				IsPrimary:    field.IsPrimary,
				Description:  field.Description,
			})
		}
		grpcTables[tableName] = &datasourcev1.TableSchema{Fields: grpcFields}
	}

	return &datasourcev1.SchemaResult{Tables: grpcTables}, nil
}

func (s *server) HealthCheck(ctx context.Context, req *datasourcev1.HealthCheckRequest) (*datasourcev1.HealthCheckResponse, error) {
	err := s.manager.HealthCheck(ctx)
	if err != nil {
		slog.Warn("插件健康检查失败", "error", err)
		return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_NOT_SERVING}, nil
	}
	return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_SERVING}, nil
}

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

	sqliteManager := sqlite.NewManager(adminConfigService)
	if err := sqliteManager.InitForBiz(context.Background(), *instanceDir, *bizNameFlag); err != nil {
		slog.Error("插件初始化业务失败", "biz", *bizNameFlag, "error", err)
		os.Exit(1)
	}
	slog.Info("成功初始化业务数据", "biz", *bizNameFlag)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *portFlag))
	if err != nil {
		slog.Error("gRPC 服务监听端口失败", "port", *portFlag, "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
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
