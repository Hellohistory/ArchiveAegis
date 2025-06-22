// Package sqlite file: internal/adapter/datasource/sqlite/manager.go
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	"golang.org/x/sync/errgroup"
	_ "modernc.org/sqlite"
)

// 断言 *Manager 实现新的 port.Executor 接口，编译期校验
var _ port.Executor = (*Manager)(nil)

const (
	debounceDuration = 2 * time.Second
)

// Manager 是 SQLite 数据源适配器的核心结构体。
// 它现在实现了 port.Executor 接口，通过统一的 Execute 方法处理所有请求。
type Manager struct {
	mu sync.RWMutex

	root          string
	group         map[string]map[string]*sql.DB
	dbSchemaCache map[*sql.DB]*dbPhysicalSchemaInfo
	schema        map[string]map[string][]string
	eventTimers   map[string]*time.Timer
	eventTimersMu sync.Mutex
	configService port.QueryAdminConfigService
}

// NewManager 创建一个新的 Manager 实例。
func NewManager(cfgService port.QueryAdminConfigService) *Manager {
	if cfgService == nil {
		log.Fatal("[DBManager] 致命错误: QueryAdminConfigService 实例不能为 nil。")
	}
	m := &Manager{
		group:         make(map[string]map[string]*sql.DB),
		dbSchemaCache: make(map[*sql.DB]*dbPhysicalSchemaInfo),
		schema:        make(map[string]map[string][]string),
		eventTimers:   make(map[string]*time.Timer),
		configService: cfgService,
	}
	return m
}

// Execute 是适配“永恒契约”的统一执行入口
func (m *Manager) Execute(ctx context.Context, req *v1.RequestEnvelope) (*v1.ResponseEnvelope, error) {
	slog.Debug("内置 SQLite 执行器收到 Execute 请求", "request_id", req.RequestId, "biz", req.BizName, "payload_type", req.Payload.TypeUrl)

	var responsePayload proto.Message
	var err error

	// 通过检查 Payload 的类型 URL 来决定执行何种操作
	switch req.Payload.TypeUrl {
	case _typeUrl(&v1.DataQueryRequest{}):
		responsePayload, err = m.handleDataQuery(ctx, req)
	case _typeUrl(&v1.DataMutateRequest{}):
		responsePayload, err = m.handleDataMutate(ctx, req)
	case _typeUrl(&v1.GetSchemaRequest{}):
		responsePayload, err = m.handleGetSchema(ctx, req)
	default:
		err = status.Errorf(codes.Unimplemented, "内置 SQLite 执行器不支持的载荷类型: %s", req.Payload.TypeUrl)
	}

	// 根据处理结果构建统一的 ResponseEnvelope
	if err != nil {
		slog.Error("内置 SQLite 执行器执行失败", "request_id", req.RequestId, "error", err)
		st, _ := status.FromError(err)
		return &v1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status: &v1.Status{
				Code:    int32(st.Code()),
				Message: st.Message(),
			},
		}, nil
	}

	// 将成功的业务结果载荷打包到 Any 中
	packedPayload, packErr := anypb.New(responsePayload)
	if packErr != nil {
		slog.Error("内置 SQLite 执行器打包响应载荷失败", "request_id", req.RequestId, "error", packErr)
		return &v1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status: &v1.Status{
				Code:    int32(codes.Internal),
				Message: fmt.Sprintf("打包响应载荷失败: %v", packErr),
			},
		}, nil
	}

	return &v1.ResponseEnvelope{
		RequestId: req.RequestId,
		Status:    &v1.Status{Code: int32(codes.OK), Message: "Success"},
		Payload:   packedPayload,
	}, nil
}

// Close 安全地关闭由 Manager 管理的所有数据库连接。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for bizName, libs := range m.group {
		for libName, db := range libs {
			if err := db.Close(); err != nil {
				log.Printf("ERROR: Closing database %s/%s failed: %v", bizName, libName, err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	m.group = make(map[string]map[string]*sql.DB)
	m.dbSchemaCache = make(map[*sql.DB]*dbPhysicalSchemaInfo)
	return firstErr
}

// Type 实现 port.Executor.Type 接口，返回适配器类型。
func (m *Manager) Type() string {
	return "sqlite_builtin"
}

// Summary 返回一个映射，表示每个业务组 (bizName) 下有哪些库文件 (libName)。
func (m *Manager) Summary() map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	summaryMap := make(map[string][]string, len(m.group))
	for bizName, libsInBiz := range m.group {
		if len(libsInBiz) > 0 {
			libNames := make([]string, 0, len(libsInBiz))
			for libName := range libsInBiz {
				libNames = append(libNames, libName)
			}
			sort.Strings(libNames)
			summaryMap[bizName] = libNames
		}
	}
	return summaryMap
}

// _typeUrl 是一个辅助函数，用于获取 Protobuf 消息的类型 URL
func _typeUrl(m proto.Message) string {
	return "type.googleapis.com/" + string(m.ProtoReflect().Descriptor().FullName())
}

// --- Query 逻辑 ---

type queryParam struct {
	Field string
	Value string
	Logic string
	Fuzzy bool
}

func (m *Manager) handleDataQuery(ctx context.Context, req *v1.RequestEnvelope) (proto.Message, error) {
	var queryReq v1.DataQueryRequest
	if err := req.Payload.UnmarshalTo(&queryReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 DataQueryRequest 失败: %v", err)
	}

	queryMap := queryReq.GetQuery().AsMap()
	tableName, ok := queryMap["table"].(string)
	if !ok || tableName == "" {
		return nil, status.Error(codes.InvalidArgument, "无效请求: query 体必须包含一个有效的 'table' 字符串字段")
	}

	args := struct {
		tableName      string
		queryParams    []queryParam
		fieldsToReturn []string
		page           int
		size           int
	}{
		tableName: tableName,
		page:      1,
		size:      50,
	}

	if pageF, ok := queryMap["page"].(float64); ok {
		args.page = int(pageF)
	}
	if sizeF, ok := queryMap["size"].(float64); ok {
		args.size = int(sizeF)
	}
	if filters, ok := queryMap["filters"].([]interface{}); ok {
		for i, f := range filters {
			filterMap, ok := f.(map[string]interface{})
			if !ok {
				return nil, status.Errorf(codes.InvalidArgument, "无效请求: filters 数组的第 %d 个元素不是一个有效的JSON对象", i)
			}

			param := queryParam{}
			if param.Field, ok = filterMap["field"].(string); !ok || param.Field == "" {
				return nil, status.Error(codes.InvalidArgument, "无效请求: filter 对象缺少或 'field' 字段类型不正确")
			}
			param.Value = fmt.Sprintf("%v", filterMap["value"])
			param.Logic, _ = filterMap["logic"].(string)
			param.Fuzzy, _ = filterMap["fuzzy"].(bool)
			args.queryParams = append(args.queryParams, param)
		}
	}
	if fields, ok := queryMap["fields_to_return"].([]interface{}); ok {
		for _, field := range fields {
			if fStr, ok := field.(string); ok {
				args.fieldsToReturn = append(args.fieldsToReturn, fStr)
			}
		}
	}

	results, total, err := m.queryInternal(ctx, req.BizName, args)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询数据失败: %v", err)
	}

	resultData, err := structpb.NewStruct(map[string]interface{}{
		"items": results,
		"total": total,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "序列化查询结果失败: %v", err)
	}

	return &v1.DataQueryResult{Data: resultData}, nil
}

func (m *Manager) queryInternal(ctx context.Context, bizName string, args struct {
	tableName      string
	queryParams    []queryParam
	fieldsToReturn []string
	page           int
	size           int
}) ([]map[string]any, int64, error) {
	bizAdminConfig, err := m.configService.GetBizQueryConfig(ctx, bizName)
	if err != nil {
		return nil, 0, fmt.Errorf("业务 '%s' 查询配置不可用: %w", bizName, err)
	}
	if bizAdminConfig == nil {
		return nil, 0, port.ErrBizNotFound
	}
	if !bizAdminConfig.IsPubliclySearchable {
		return nil, 0, port.ErrPermissionDenied
	}

	targetTableName := args.tableName
	if targetTableName == "" {
		targetTableName = bizAdminConfig.DefaultQueryTable
	}
	if targetTableName == "" {
		return nil, 0, fmt.Errorf("业务 '%s' 未能确定查询目标表", bizName)
	}

	tableAdminConfig, tableConfigExists := bizAdminConfig.Tables[targetTableName]
	if !tableConfigExists {
		return nil, 0, port.ErrTableNotFoundInBiz
	}
	if !tableAdminConfig.IsSearchable {
		return nil, 0, port.ErrPermissionDenied
	}

	validatedQueryParams := make([]queryParam, 0, len(args.queryParams))
	for _, p := range args.queryParams {
		fieldSetting, fieldExists := tableAdminConfig.Fields[p.Field]
		if !fieldExists || !fieldSetting.IsSearchable {
			return nil, 0, fmt.Errorf("字段 '%s' 无效或不可搜索", p.Field)
		}
		validatedQueryParams = append(validatedQueryParams, p)
	}

	var selectFieldsForSQL []string
	if len(args.fieldsToReturn) > 0 {
		for _, fieldName := range args.fieldsToReturn {
			fieldSetting, fieldExists := tableAdminConfig.Fields[fieldName]
			if !fieldExists || !fieldSetting.IsReturnable {
				return nil, 0, fmt.Errorf("安全策略冲突：字段 '%s' 未被授权返回", fieldName)
			}
			selectFieldsForSQL = append(selectFieldsForSQL, fieldName)
		}
	} else {
		for fieldName, fieldSetting := range tableAdminConfig.Fields {
			if fieldSetting.IsReturnable {
				selectFieldsForSQL = append(selectFieldsForSQL, fieldName)
			}
		}
	}

	if len(selectFieldsForSQL) == 0 {
		return nil, 0, fmt.Errorf("在表 '%s' 的配置中，没有找到任何可供返回的字段", targetTableName)
	}
	sort.Strings(selectFieldsForSQL)

	m.mu.RLock()
	dbInstancesInBiz, bizGroupExists := m.group[bizName]
	m.mu.RUnlock()
	if !bizGroupExists || len(dbInstancesInBiz) == 0 {
		return []map[string]any{}, 0, nil
	}

	var totalCount int64
	resultsChannel := make(chan []map[string]any, len(dbInstancesInBiz))
	g, queryCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		countGroup, countCtx := errgroup.WithContext(queryCtx)
		for _, db := range dbInstancesInBiz {
			currentDB := db
			countGroup.Go(func() error {
				countSQL, countArgs, errBuild := buildCountSQL(targetTableName, validatedQueryParams)
				if errBuild != nil {
					return fmt.Errorf("构建COUNT查询失败: %w", errBuild)
				}
				var localCount int64
				errScan := currentDB.QueryRowContext(countCtx, countSQL, countArgs...).Scan(&localCount)
				if errScan != nil {
					slog.Warn("[DBManager Query] 计算总数时部分库查询失败 (不影响总结果)", "error", errScan)
					return nil
				}
				atomic.AddInt64(&totalCount, localCount)
				return nil
			})
		}
		return countGroup.Wait()
	})

	g.Go(func() error {
		defer close(resultsChannel)
		dataGroup, dataCtx := errgroup.WithContext(queryCtx)
		sem := make(chan struct{}, runtime.NumCPU())

		for libName, dbConn := range dbInstancesInBiz {
			m.mu.RLock()
			physicalSchemaInfo, hasPhysicalSchema := m.dbSchemaCache[dbConn]
			m.mu.RUnlock()
			if !hasPhysicalSchema || physicalSchemaInfo == nil {
				continue
			}
			if _, tablePhysicallyExists := physicalSchemaInfo.allTablesAndColumns[targetTableName]; !tablePhysicallyExists {
				continue
			}

			currentLibName, currentDBConn := libName, dbConn
			dataGroup.Go(func() error {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-dataCtx.Done():
					return dataCtx.Err()
				}

				sqlQuery, queryArgs, errBuild := buildQuerySQL(targetTableName, selectFieldsForSQL, validatedQueryParams, args.page, args.size)
				if errBuild != nil {
					slog.Error("[DBManager Query] 构建SQL失败，已跳过此库", "error", errBuild)
					return nil
				}

				rows, errExec := currentDBConn.QueryContext(dataCtx, sqlQuery, queryArgs...)
				if errExec != nil {
					return fmt.Errorf("查询库 '%s/%s' 表 '%s' 失败: %w", bizName, currentLibName, targetTableName, errExec)
				}
				defer rows.Close()

				actualReturnedColumns, _ := rows.Columns()
				var libResults []map[string]any
				for rows.Next() {
					scanDest := make([]any, len(actualReturnedColumns))
					scanDestPtrs := make([]any, len(actualReturnedColumns))
					for i := range scanDest {
						scanDestPtrs[i] = &scanDest[i]
					}
					if errScan := rows.Scan(scanDestPtrs...); errScan != nil {
						slog.Warn("[DBManager Query] 扫描库行数据失败，跳过此行", "biz", bizName, "lib", currentLibName, "error", errScan)
						continue
					}

					rowData := map[string]any{"__lib": currentLibName}
					for i, colName := range actualReturnedColumns {
						if bytes, ok := scanDest[i].([]byte); ok {
							rowData[colName] = string(bytes)
						} else {
							rowData[colName] = scanDest[i]
						}
					}
					libResults = append(libResults, rowData)
				}
				if errRows := rows.Err(); errRows != nil {
					return fmt.Errorf("迭代库 '%s/%s' 表 '%s' 行数据时发生错误: %w", bizName, currentLibName, targetTableName, errRows)
				}
				if len(libResults) > 0 {
					resultsChannel <- libResults
				}
				return nil
			})
		}
		return dataGroup.Wait()
	})

	allAggregatedResults := make([]map[string]any, 0)
	for resSlice := range resultsChannel {
		allAggregatedResults = append(allAggregatedResults, resSlice...)
	}

	if err := g.Wait(); err != nil {
		slog.Error("[DBManager Query] 查询中发生错误", "biz", bizName, "table", targetTableName, "error", err)
		return allAggregatedResults, totalCount, fmt.Errorf("查询业务 '%s' 的表 '%s' 时发生部分错误: %w", bizName, targetTableName, err)
	}

	return allAggregatedResults, totalCount, nil
}

// --- Mutate 逻辑 ---

func (m *Manager) handleDataMutate(ctx context.Context, req *v1.RequestEnvelope) (proto.Message, error) {
	var mutateReq v1.DataMutateRequest
	if err := req.Payload.UnmarshalTo(&mutateReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 DataMutateRequest 失败: %v", err)
	}

	goResult, err := m.mutateInternal(ctx, port.MutateRequest{
		BizName:   req.BizName,
		Operation: mutateReq.Operation,
		Payload:   mutateReq.GetPayload().AsMap(),
	})
	if err != nil {
		// 修复：使用 status.Errorf 包装错误，而不是调用不存在的 .Err()
		return nil, status.Errorf(codes.Internal, "写操作执行失败: %v", err)
	}

	resultData, err := structpb.NewStruct(goResult.Data)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "序列化写操作结果失败: %v", err)
	}
	return &v1.DataMutateResult{Data: resultData}, nil
}

func (m *Manager) mutateInternal(ctx context.Context, req port.MutateRequest) (*port.MutateResult, error) {
	bizAdminConfig, err := m.configService.GetBizQueryConfig(ctx, req.BizName)
	if err != nil {
		return nil, fmt.Errorf("业务 '%s' 查询配置不可用: %w", req.BizName, err)
	}
	if bizAdminConfig == nil {
		return nil, port.ErrBizNotFound
	}

	payload := req.Payload
	tableName, ok := payload["table_name"].(string)
	if !ok || tableName == "" {
		return nil, status.Error(codes.InvalidArgument, "写操作的 payload 中必须包含一个有效的 'table_name' 字符串字段")
	}

	tableConfig, exists := bizAdminConfig.Tables[tableName]
	if !exists {
		return nil, port.ErrTableNotFoundInBiz
	}

	var opAllowed bool
	var sqlStmt string
	var args []interface{}

	switch req.Operation {
	case "create":
		opAllowed = tableConfig.AllowCreate
		if opAllowed {
			data, ok := payload["data"].(map[string]interface{})
			if !ok {
				return nil, status.Error(codes.InvalidArgument, "create 操作的 payload 中必须包含一个有效的 'data' 对象")
			}
			sqlStmt, args, err = buildInsertSQL(tableName, data)
		}
	case "update":
		opAllowed = tableConfig.AllowUpdate
		if opAllowed {
			data, ok := payload["data"].(map[string]interface{})
			if !ok {
				return nil, status.Error(codes.InvalidArgument, "update 操作的 payload 中必须包含一个有效的 'data' 对象")
			}
			filters, parseErr := parseFiltersFromPayload(payload)
			if parseErr != nil {
				return nil, parseErr
			}
			sqlStmt, args, err = buildUpdateSQL(tableName, data, filters)
		}
	case "delete":
		opAllowed = tableConfig.AllowDelete
		if opAllowed {
			filters, parseErr := parseFiltersFromPayload(payload)
			if parseErr != nil {
				return nil, parseErr
			}
			sqlStmt, args, err = buildDeleteSQL(tableName, filters)
		}
	default:
		return nil, status.Errorf(codes.Unimplemented, "不支持的写操作类型: '%s'", req.Operation)
	}

	if !opAllowed {
		return nil, port.ErrPermissionDenied
	}
	if err != nil {
		return nil, fmt.Errorf("构建写操作SQL失败: %w", err)
	}

	m.mu.RLock()
	dbInstances, bizExists := m.group[req.BizName]
	m.mu.RUnlock()
	if !bizExists {
		return nil, port.ErrBizNotFound
	}

	var totalRowsAffected int64
	for libName, db := range dbInstances {
		res, execErr := db.ExecContext(ctx, sqlStmt, args...)
		if execErr != nil {
			errMsg := fmt.Errorf("操作在库 '%s' 上失败并已中止。错误: %w", libName, execErr)
			slog.Error("[DBManager Mutate]", "error", errMsg)
			return nil, errMsg
		}
		rowsAffected, _ := res.RowsAffected()
		totalRowsAffected += rowsAffected
	}

	return &port.MutateResult{
		Data: map[string]interface{}{
			"success":       true,
			"rows_affected": totalRowsAffected,
			"message":       "操作成功在所有相关库上执行。",
		},
		Source: m.Type(),
	}, nil
}

func parseFiltersFromPayload(payload map[string]interface{}) ([]queryParam, error) {
	var filters []queryParam
	rawFilters, ok := payload["filters"].([]interface{})
	if !ok {
		return filters, nil
	}
	for i, f := range rawFilters {
		filterMap, ok := f.(map[string]interface{})
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "无效请求: filters 数组的第 %d 个元素不是一个有效的JSON对象", i)
		}
		param := queryParam{}
		if param.Field, ok = filterMap["field"].(string); !ok || param.Field == "" {
			return nil, status.Error(codes.InvalidArgument, "无效请求: filter 对象缺少或 'field' 字段类型不正确")
		}
		if val, exists := filterMap["value"]; exists {
			param.Value = fmt.Sprintf("%v", val)
		}
		param.Logic, _ = filterMap["logic"].(string)
		param.Fuzzy, _ = filterMap["fuzzy"].(bool)
		filters = append(filters, param)
	}
	return filters, nil
}

// --- Schema 逻辑 ---

func (m *Manager) handleGetSchema(ctx context.Context, req *v1.RequestEnvelope) (proto.Message, error) {
	var schemaReq v1.GetSchemaRequest
	if err := req.Payload.UnmarshalTo(&schemaReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 GetSchemaRequest 失败: %v", err)
	}

	result, err := m.getSchemaInternal(ctx, port.SchemaRequest{
		BizName:   req.BizName,
		TableName: schemaReq.TableName,
	})
	if err != nil {
		// 修复：使用 status.Errorf 包装错误，而不是调用不存在的 .Err()
		return nil, status.Errorf(codes.Internal, "获取 Schema 失败: %v", err)
	}

	grpcTables := make(map[string]*v1.TableSchema)
	for tableName, tableSchema := range result.Tables {
		var grpcFields []*v1.FieldDescription
		for _, field := range tableSchema {
			grpcFields = append(grpcFields, &v1.FieldDescription{
				Name:         field.Name,
				DataType:     field.DataType,
				IsSearchable: field.IsSearchable,
				IsReturnable: field.IsReturnable,
				IsPrimary:    field.IsPrimary,
				Description:  field.Description,
			})
		}
		grpcTables[tableName] = &v1.TableSchema{Fields: grpcFields}
	}

	return &v1.SchemaResult{Tables: grpcTables}, nil
}

func (m *Manager) getSchemaInternal(ctx context.Context, req port.SchemaRequest) (*port.SchemaResult, error) {
	bizConfig, err := m.configService.GetBizQueryConfig(ctx, req.BizName)
	if err != nil {
		return nil, fmt.Errorf("获取业务 '%s' 的 schema 配置失败: %w", req.BizName, err)
	}
	if bizConfig == nil {
		return nil, port.ErrBizNotFound
	}

	schemaTables := make(map[string][]port.FieldDescription)
	for tableName, tableConfig := range bizConfig.Tables {
		if req.TableName != "" && req.TableName != tableName {
			continue
		}
		var fields []port.FieldDescription
		for _, fieldSetting := range tableConfig.Fields {
			fields = append(fields, port.FieldDescription{
				Name:         fieldSetting.FieldName,
				DataType:     fieldSetting.DataType,
				IsSearchable: fieldSetting.IsSearchable,
				IsReturnable: fieldSetting.IsReturnable,
				IsPrimary:    false,
				Description:  "",
			})
		}
		sort.Slice(fields, func(i, j int) bool {
			return fields[i].Name < fields[j].Name
		})
		schemaTables[tableName] = fields
	}

	if req.TableName != "" && len(schemaTables) == 0 {
		return nil, port.ErrTableNotFoundInBiz
	}

	return &port.SchemaResult{Tables: schemaTables}, nil
}
