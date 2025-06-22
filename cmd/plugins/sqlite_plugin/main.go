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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	_ "modernc.org/sqlite"
)

//go:embed README.md
var pluginDescription string

const pluginVersion = "2.0.0" // 版本升级以反映契约变更

// server 结构体现在实现了新版 DataSourceServer 接口
type server struct {
	datasourcev1.UnimplementedDataSourceServer
	manager    port.DataSource // 底层 manager 保持不变，实现了核心业务逻辑
	pluginName string
	bizName    string
}

// GetPluginInfo 方法实现，现在返回更丰富的能力清单
func (s *server) GetPluginInfo(ctx context.Context, req *datasourcev1.GetPluginInfoRequest) (*datasourcev1.GetPluginInfoResponse, error) {
	slog.Info("插件收到 GetPluginInfo 请求")
	return &datasourcev1.GetPluginInfoResponse{
		Name:                s.pluginName,
		Version:             pluginVersion,
		Type:                "SQL",
		DescriptionMarkdown: pluginDescription,
		ContractVersion: &datasourcev1.ApiVersion{
			Major: 1, // 契约主版本
			Minor: 0,
			Patch: 0,
		},
		// 明确声明本插件支持的载荷类型
		SupportedPayloads: []string{
			_typeUrl(&datasourcev1.DataQueryRequest{}),
			_typeUrl(&datasourcev1.DataMutateRequest{}),
			_typeUrl(&datasourcev1.GetSchemaRequest{}),
		},
		SupportedCapabilities: []string{"AGGREGATION"}, // 示例能力
	}, nil
}

// Execute 是新契约下的统一执行入口
func (s *server) Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	slog.Info("插件收到 Execute 请求", "request_id", req.RequestId, "biz", req.BizName, "payload_type", req.Payload.TypeUrl)

	var responsePayload proto.Message
	var err error

	// 关键：通过检查 Payload 的类型 URL 来决定执行何种操作
	switch req.Payload.TypeUrl {
	case _typeUrl(&datasourcev1.DataQueryRequest{}):
		responsePayload, err = s.handleDataQuery(ctx, req)
	case _typeUrl(&datasourcev1.DataMutateRequest{}):
		responsePayload, err = s.handleDataMutate(ctx, req)
	case _typeUrl(&datasourcev1.GetSchemaRequest{}):
		responsePayload, err = s.handleGetSchema(ctx, req)
	default:
		err = status.Errorf(codes.Unimplemented, "不支持的载荷类型: %s", req.Payload.TypeUrl)
	}

	// 根据处理结果构建统一的 ResponseEnvelope
	if err != nil {
		slog.Error("插件执行失败", "request_id", req.RequestId, "error", err)
		st, _ := status.FromError(err)
		return &datasourcev1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status: &datasourcev1.Status{
				Code:    int32(st.Code()),
				Message: st.Message(),
			},
		}, nil // gRPC层面返回nil错误，业务错误在Status中体现
	}

	// 将成功的业务结果载荷打包到 Any 中
	packedPayload, packErr := anypb.New(responsePayload)
	if packErr != nil {
		slog.Error("打包响应载荷失败", "request_id", req.RequestId, "error", packErr)
		return &datasourcev1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status: &datasourcev1.Status{
				Code:    int32(codes.Internal),
				Message: fmt.Sprintf("打包响应载荷失败: %v", packErr),
			},
		}, nil
	}

	return &datasourcev1.ResponseEnvelope{
		RequestId: req.RequestId,
		Status:    &datasourcev1.Status{Code: int32(codes.OK), Message: "Success"},
		Payload:   packedPayload,
	}, nil
}

func (s *server) HealthCheck(ctx context.Context, req *datasourcev1.HealthCheckRequest) (*datasourcev1.HealthCheckResponse, error) {
	err := s.manager.HealthCheck(ctx)
	if err != nil {
		slog.Warn("插件健康检查失败", "error", err)
		return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_NOT_SERVING}, nil
	}
	return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_SERVING}, nil
}

// =============================================================================
//  具体载荷处理函数 (从旧的 RPC 方法迁移而来)
// =============================================================================

func (s *server) handleDataQuery(ctx context.Context, req *datasourcev1.RequestEnvelope) (proto.Message, error) {
	// 1. 解包
	var queryReq datasourcev1.DataQueryRequest
	if err := req.Payload.UnmarshalTo(&queryReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 DataQueryRequest 失败: %v", err)
	}

	// 2. 转换为核心业务请求
	goReq := port.QueryRequest{
		BizName: req.BizName,
		Query:   queryReq.GetQuery().AsMap(),
	}

	// 3. 调用核心逻辑
	result, err := s.manager.Query(ctx, goReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询数据失败: %v", err)
	}

	// 4. 将结果转换为 Protobuf 消息
	resultData, err := structpb.NewStruct(result.Data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "序列化查询结果失败: %v", err)
	}

	return &datasourcev1.DataQueryResult{Data: resultData}, nil
}

func (s *server) handleDataMutate(ctx context.Context, req *datasourcev1.RequestEnvelope) (proto.Message, error) {
	// 1. 解包
	var mutateReq datasourcev1.DataMutateRequest
	if err := req.Payload.UnmarshalTo(&mutateReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 DataMutateRequest 失败: %v", err)
	}

	// 2. 转换为核心业务请求
	goReq := port.MutateRequest{
		BizName:   req.BizName,
		Operation: mutateReq.Operation,
		Payload:   mutateReq.GetPayload().AsMap(),
	}

	// 3. 调用核心逻辑
	goResult, err := s.manager.Mutate(ctx, goReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "写操作失败: %v", err)
	}

	// 4. 将结果转换为 Protobuf 消息
	resultData, err := structpb.NewStruct(goResult.Data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "序列化写操作结果失败: %v", err)
	}
	return &datasourcev1.DataMutateResult{Data: resultData}, nil
}

func (s *server) handleGetSchema(ctx context.Context, req *datasourcev1.RequestEnvelope) (proto.Message, error) {
	// 1. 解包
	var schemaReq datasourcev1.GetSchemaRequest
	if err := req.Payload.UnmarshalTo(&schemaReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 GetSchemaRequest 失败: %v", err)
	}

	// 2. 转换为核心业务请求
	goReq := port.SchemaRequest{
		BizName:   req.BizName,
		TableName: schemaReq.TableName,
	}

	// 3. 调用核心逻辑
	result, err := s.manager.GetSchema(ctx, goReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "获取 Schema 失败: %v", err)
	}

	// 4. 将结果转换为 Protobuf 消息
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

// _typeUrl 是一个辅助函数，用于获取 Protobuf 消息的类型 URL
func _typeUrl(m proto.Message) string {
	return "type.googleapis.com/" + string(m.ProtoReflect().Descriptor().FullName())
}

// main 函数基本保持不变，只是注册的服务不同
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

	// --- 依赖初始化部分保持不变 ---
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

	// --- gRPC 服务启动部分保持不变 ---
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

	slog.Info("✅ SQLite插件(v2)启动成功，开始提供服务...")
	if err := grpcServer.Serve(lis); err != nil {
		slog.Error("gRPC 服务启动失败", "error", err)
		os.Exit(1)
	}
}

// initAuthDB 函数保持不变
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
