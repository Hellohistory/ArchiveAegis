// file: cmd/plugins/sqlite_plugin/main.go
package main

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/adapter/datasource/sqlite"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"context"
	"database/sql"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
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
	log.Println("插件收到 GetPluginInfo 请求")
	return &datasourcev1.GetPluginInfoResponse{
		Name:                s.pluginName,
		Version:             pluginVersion,
		Type:                "sqlite_plugin",
		SupportedBizNames:   []string{s.bizName}, // 告知网关它能处理哪个业务
		DescriptionMarkdown: pluginDescription,
	}, nil
}

// Query 将gRPC请求转换为内部调用，再将结果转换为gRPC响应
func (s *server) Query(ctx context.Context, req *datasourcev1.QueryRequest) (*datasourcev1.QueryResult, error) {
	log.Printf("插件收到Query请求: biz=%s, table=%s", req.BizName, req.TableName)

	goReq := port.QueryRequest{
		BizName:        req.BizName,
		TableName:      req.TableName,
		Page:           int(req.Page),
		Size:           int(req.Size),
		FieldsToReturn: req.FieldsToReturn,
	}
	for _, p := range req.QueryParams {
		goReq.QueryParams = append(goReq.QueryParams, port.QueryParam{
			Field: p.Field,
			Value: p.Value,
			Logic: p.Logic,
			Fuzzy: p.Fuzzy,
		})
	}

	result, err := s.manager.Query(ctx, goReq)
	if err != nil {
		return nil, err
	}

	// 转换Go结果到gRPC响应
	anySlice := make([]any, len(result.Data))
	for i, v := range result.Data {
		anySlice[i] = v
	}
	listValue, err := structpb.NewList(anySlice)
	if err != nil {
		return nil, fmt.Errorf("转换查询结果为ListValue失败: %w", err)
	}

	grpcResult := &datasourcev1.QueryResult{
		Data:   listValue,
		Total:  result.Total,
		Source: s.manager.Type(),
	}

	return grpcResult, nil
}

// GetSchema 的完整实现
func (s *server) GetSchema(ctx context.Context, req *datasourcev1.SchemaRequest) (*datasourcev1.SchemaResult, error) {
	log.Printf("插件收到GetSchema请求: biz=%s", req.BizName)
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

// HealthCheck 的完整实现
func (s *server) HealthCheck(ctx context.Context, req *datasourcev1.HealthCheckRequest) (*datasourcev1.HealthCheckResponse, error) {
	err := s.manager.HealthCheck(ctx)
	if err != nil {
		log.Printf("插件健康检查失败: %v", err)
		return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_NOT_SERVING}, nil
	}
	return &datasourcev1.HealthCheckResponse{Status: datasourcev1.HealthCheckResponse_SERVING}, nil
}

// Mutate 方法的完整实现
func (s *server) Mutate(ctx context.Context, req *datasourcev1.MutateRequest) (*datasourcev1.MutateResult, error) {
	log.Printf("插件收到Mutate请求: biz=%s", req.BizName)

	goReq := port.MutateRequest{
		BizName: req.BizName,
	}

	switch op := req.Operation.(type) {
	case *datasourcev1.MutateRequest_CreateOp:
		goReq.CreateOp = &port.CreateOperation{
			TableName: op.CreateOp.TableName,
			Data:      op.CreateOp.Data.AsMap(),
		}
	case *datasourcev1.MutateRequest_UpdateOp:
		filters := make([]port.QueryParam, len(op.UpdateOp.Filters))
		for i, f := range op.UpdateOp.Filters {
			filters[i] = port.QueryParam{Field: f.Field, Value: f.Value, Logic: f.Logic, Fuzzy: f.Fuzzy}
		}
		goReq.UpdateOp = &port.UpdateOperation{
			TableName: op.UpdateOp.TableName,
			Data:      op.UpdateOp.Data.AsMap(),
			Filters:   filters,
		}
	case *datasourcev1.MutateRequest_DeleteOp:
		filters := make([]port.QueryParam, len(op.DeleteOp.Filters))
		for i, f := range op.DeleteOp.Filters {
			filters[i] = port.QueryParam{Field: f.Field, Value: f.Value, Logic: f.Logic, Fuzzy: f.Fuzzy}
		}
		goReq.DeleteOp = &port.DeleteOperation{
			TableName: op.DeleteOp.TableName,
			Filters:   filters,
		}
	default:
		return nil, fmt.Errorf("收到了无效的Mutate操作类型")
	}

	goResult, err := s.manager.Mutate(ctx, goReq)
	if err != nil {
		return nil, err
	}

	grpcResult := &datasourcev1.MutateResult{
		Success:      goResult.Success,
		RowsAffected: goResult.RowsAffected,
		Message:      goResult.Message,
	}

	return grpcResult, nil
}

func main() {
	portFlag := flag.Int("port", 50051, "服务监听端口")
	bizNameFlag := flag.String("biz", "", "此插件管理的业务组名称 (必须)")
	pluginNameFlag := flag.String("name", "unnamed-sqlite-plugin", "此插件实例的唯一名称")
	instanceDir := flag.String("instance_dir", "./instance", "实例目录的路径")
	flag.Parse()

	if *bizNameFlag == "" {
		log.Fatal("必须通过 -biz 参数指定插件管理的业务组名称")
	}

	log.Println("🔌 插件开始初始化依赖...")
	authDbPath := filepath.Join(*instanceDir, "auth.db")
	pluginSysDB, err := initAuthDB(authDbPath)
	if err != nil {
		log.Fatalf("插件无法初始化认证数据库连接: %v", err)
	}
	defer pluginSysDB.Close()
	log.Println("🔌 插件成功连接到 auth.db")

	adminConfigService, err := service.NewAdminConfigServiceImpl(pluginSysDB, 100, 1*time.Minute)
	if err != nil {
		log.Fatalf("插件无法创建AdminConfigService: %v", err)
	}
	log.Println("🔌 插件成功创建 AdminConfigService")

	sqliteManager := sqlite.NewManager(adminConfigService)
	if err := sqliteManager.InitForBiz(context.Background(), *instanceDir, *bizNameFlag); err != nil {

		log.Fatalf("插件初始化业务 '%s' 失败: %v", *bizNameFlag, err)
	}

	log.Printf("🔌 插件成功初始化业务数据: %s", *bizNameFlag)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *portFlag))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	datasourcev1.RegisterDataSourceServer(grpcServer, &server{
		manager:    sqliteManager,
		pluginName: *pluginNameFlag,
		bizName:    *bizNameFlag,
	})

	log.Printf("✅ SQLite插件启动成功, 正在监听端口: %d, 管理业务组: %s", *portFlag, *bizNameFlag)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
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
