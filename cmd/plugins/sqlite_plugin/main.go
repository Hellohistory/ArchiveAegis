// file: cmd/plugins/sqlite_plugin/main.go
package main

import (
	datasourcev2 "ArchiveAegis/gen/go/datasource/v1"
	"ArchiveAegis/internal/adapter/datasource/sqlite"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
	"log"
	"net"
	"path/filepath"
	"time"
)

// server 结构体实现了 gRPC 生成的 DataSourceServer 接口
type server struct {
	datasourcev2.UnimplementedDataSourceServer
	manager port.DataSource
}

// Query 将gRPC请求转换为内部调用，再将结果转换为gRPC响应
func (s *server) Query(ctx context.Context, req *datasourcev2.QueryRequest) (*datasourcev2.QueryResult, error) {
	log.Printf("插件收到Query请求: biz=%s, table=%s", req.BizName, req.TableName)

	// 将 gRPC 请求转换为 Go 的 port.QueryRequest
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

	// 调用 Manager 核心逻辑
	result, err := s.manager.Query(ctx, goReq)
	if err != nil {
		return nil, err
	}

	// 将 Go 的结果 (*port.QueryResult) 转换为 gRPC 的 *datasourcev1.QueryResult
	var values []*structpb.Value
	for _, row := range result.Data {
		val, err := structpb.NewValue(row)
		if err != nil {
			return nil, fmt.Errorf("转换 row 为 structpb.Value 失败: %w", err)
		}
		values = append(values, val)
	}
	listValue := &structpb.ListValue{Values: values}

	grpcResult := &datasourcev2.QueryResult{
		Data:   listValue,
		Total:  result.Total,
		Source: s.manager.Type(),
	}

	return grpcResult, nil
}

// GetSchema GetSchema的完整实现
func (s *server) GetSchema(ctx context.Context, req *datasourcev2.SchemaRequest) (*datasourcev2.SchemaResult, error) {
	log.Printf("插件收到GetSchema请求: biz=%s", req.BizName)
	goReq := port.SchemaRequest{BizName: req.BizName, TableName: req.TableName}

	result, err := s.manager.GetSchema(ctx, goReq)
	if err != nil {
		return nil, err
	}

	grpcTables := make(map[string]*datasourcev2.TableSchema)
	for tableName, tableSchema := range result.Tables {
		var grpcFields []*datasourcev2.FieldDescription
		for _, field := range tableSchema {
			grpcFields = append(grpcFields, &datasourcev2.FieldDescription{
				Name:         field.Name,
				DataType:     field.DataType,
				IsSearchable: field.IsSearchable,
				IsReturnable: field.IsReturnable,
				IsPrimary:    field.IsPrimary,
				Description:  field.Description,
			})
		}
		grpcTables[tableName] = &datasourcev2.TableSchema{Fields: grpcFields}
	}

	return &datasourcev2.SchemaResult{Tables: grpcTables}, nil
}

// HealthCheck HealthCheck的完整实现
func (s *server) HealthCheck(ctx context.Context, req *datasourcev2.HealthCheckRequest) (*datasourcev2.HealthCheckResponse, error) {
	err := s.manager.HealthCheck(ctx)
	if err != nil {
		log.Printf("插件健康检查失败: %v", err)
		return &datasourcev2.HealthCheckResponse{Status: datasourcev2.HealthCheckResponse_NOT_SERVING}, nil
	}
	return &datasourcev2.HealthCheckResponse{Status: datasourcev2.HealthCheckResponse_SERVING}, nil
}

// Mutate 方法的存根实现
func (s *server) Mutate(ctx context.Context, req *datasourcev2.MutateRequest) (*datasourcev2.MutateResult, error) {
	log.Printf("插件收到Mutate请求: biz=%s", req.BizName)
	// 实际实现时，需要将 gRPC MutateRequest 转换为 port.MutateRequest，然后调用 s.manager.Mutate
	return &datasourcev2.MutateResult{Success: false, Message: "Mutate API not implemented in plugin yet"}, nil
}

func main() {
	portFlag := flag.Int("port", 50051, "The server port")
	bizName := flag.String("biz", "", "Business group name this plugin manages")
	instanceDir := flag.String("instance_dir", "./instance", "Path to instance directory")
	flag.Parse()

	if *bizName == "" {
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
	if err := sqliteManager.InitForBiz(context.Background(), *instanceDir, *bizName); err != nil {
		log.Fatalf("插件初始化业务 '%s' 失败: %v", *bizName, err)
	}
	log.Printf("🔌 插件成功初始化业务数据: %s", *bizName)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *portFlag))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()

	datasourcev2.RegisterDataSourceServer(grpcServer, &server{manager: sqliteManager})

	log.Printf("✅ SQLite插件启动成功，正在监听端口: %d，管理业务组: %s", *portFlag, *bizName)
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
