// file: internal/adapter/datasource/sqlite/manager_query.go

// Package sqlite 实现了内置的、基于文件的 SQLite 数据源执行器。
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type queryParam struct {
	Field string
	Value string
	Logic string
	Fuzzy bool
}

// rawQueryArgs 用于封装从 handleDataQuery 解析出的原始查询参数。
type rawQueryArgs struct {
	tableName      string
	queryParams    []queryParam
	fieldsToReturn []string
	page           int
	size           int
}

// validatedQueryParams 用于封装通过权限和规则校验后的、可安全执行的查询参数。
type validatedQueryParams struct {
	tableName    string
	queryParams  []queryParam
	selectFields []string
	page         int
	size         int
}

// handleDataQuery 负责解析请求并调用核心查询逻辑。
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

	args := rawQueryArgs{
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

	itemsAsInterface := make([]interface{}, len(results))
	for i, item := range results {
		itemsAsInterface[i] = item
	}

	resultData, err := structpb.NewStruct(map[string]interface{}{
		"items": itemsAsInterface,
		"total": total,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "序列化查询结果失败: %v", err)
	}

	return &v1.DataQueryResult{Data: resultData}, nil
}

// queryInternal 现在负责在获取所有结果后，于内存中进行健壮的、类型安全的排序和分页。
func (m *Manager) queryInternal(ctx context.Context, bizName string, args rawQueryArgs) ([]map[string]any, int64, error) {
	validatedParams, err := m.validateQueryRequest(ctx, bizName, args)
	if err != nil {
		return nil, 0, err
	}

	m.mu.RLock()
	dbInstancesInBiz, bizGroupExists := m.group[bizName]
	m.mu.RUnlock()
	if !bizGroupExists || len(dbInstancesInBiz) == 0 {
		return []map[string]any{}, 0, nil
	}

	allAggregatedResults, totalCount, err := m.executeConcurrentQuery(ctx, bizName, dbInstancesInBiz, validatedParams)
	if err != nil {
		return nil, totalCount, err
	}

	sort.SliceStable(allAggregatedResults, func(i, j int) bool {
		valI, iExists := allAggregatedResults[i]["id"]
		valJ, jExists := allAggregatedResults[j]["id"]

		if !iExists || valI == nil {
			return false
		}
		if !jExists || valJ == nil {
			return true
		}

		switch vI := valI.(type) {
		case int64:
			if vJ, ok := valJ.(int64); ok {
				return vI < vJ
			}
		case float64:
			if vJ, ok := valJ.(float64); ok {
				return vI < vJ
			}
		case string:
			if vJ, ok := valJ.(string); ok {
				return vI < vJ
			}
		}

		return fmt.Sprintf("%v", valI) < fmt.Sprintf("%v", valJ)
	})

	start := (validatedParams.page - 1) * validatedParams.size
	if start < 0 || start > len(allAggregatedResults) {
		return []map[string]any{}, totalCount, nil
	}

	end := start + validatedParams.size
	if end > len(allAggregatedResults) {
		end = len(allAggregatedResults)
	}

	return allAggregatedResults[start:end], totalCount, nil
}

// validateQueryRequest 负责所有查询前的权限和参数校验。
func (m *Manager) validateQueryRequest(ctx context.Context, bizName string, args rawQueryArgs) (*validatedQueryParams, error) {
	bizAdminConfig, err := m.configService.GetBizQueryConfig(ctx, bizName)
	if err != nil {
		return nil, fmt.Errorf("业务 '%s' 查询配置不可用: %w", bizName, err)
	}
	if bizAdminConfig == nil {
		return nil, port.ErrBizNotFound
	}
	if !bizAdminConfig.IsPubliclySearchable {
		return nil, port.ErrPermissionDenied
	}

	targetTableName := args.tableName
	if targetTableName == "" {
		targetTableName = bizAdminConfig.DefaultQueryTable
	}
	if targetTableName == "" {
		return nil, fmt.Errorf("业务 '%s' 未能确定查询目标表", bizName)
	}

	tableAdminConfig, tableConfigExists := bizAdminConfig.Tables[targetTableName]
	if !tableConfigExists {
		return nil, port.ErrTableNotFoundInBiz
	}
	if !tableAdminConfig.IsSearchable {
		return nil, port.ErrPermissionDenied
	}

	validatedParamsSlice := make([]queryParam, 0, len(args.queryParams))
	for _, p := range args.queryParams {
		fieldSetting, fieldExists := tableAdminConfig.Fields[p.Field]
		if !fieldExists || !fieldSetting.IsSearchable {
			return nil, fmt.Errorf("字段 '%s' 无效或不可搜索", p.Field)
		}
		validatedParamsSlice = append(validatedParamsSlice, p)
	}

	var selectFieldsForSQL []string
	if len(args.fieldsToReturn) > 0 {
		for _, fieldName := range args.fieldsToReturn {
			fieldSetting, fieldExists := tableAdminConfig.Fields[fieldName]
			if !fieldExists || !fieldSetting.IsReturnable {
				return nil, fmt.Errorf("安全策略冲突：字段 '%s' 未被授权返回", fieldName)
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
		return nil, fmt.Errorf("在表 '%s' 的配置中，没有找到任何可供返回的字段", targetTableName)
	}
	sort.Strings(selectFieldsForSQL)

	return &validatedQueryParams{
		tableName:    targetTableName,
		queryParams:  validatedParamsSlice,
		selectFields: selectFieldsForSQL,
		page:         args.page,
		size:         args.size,
	}, nil
}

// executeConcurrentQuery 负责并发地从多个数据库实例中获取数据和总数。
func (m *Manager) executeConcurrentQuery(ctx context.Context, bizName string, dbInstances map[string]*dbInstance, params *validatedQueryParams) ([]map[string]any, int64, error) {
	var totalCount int64
	resultsChannel := make(chan []map[string]any, len(dbInstances))
	g, queryCtx := errgroup.WithContext(ctx)

	// 并发计算总数
	g.Go(func() error {
		countGroup, countCtx := errgroup.WithContext(queryCtx)
		for _, instance := range dbInstances {
			m.mu.RLock()
			physicalSchemaInfo, hasPhysicalSchema := m.dbSchemaCache[instance.conn]
			m.mu.RUnlock()
			if !hasPhysicalSchema || physicalSchemaInfo == nil {
				continue
			}
			if _, tablePhysicallyExists := physicalSchemaInfo.allTablesAndColumns[params.tableName]; !tablePhysicallyExists {
				continue
			}

			currentDB := instance.conn
			countGroup.Go(func() error {
				countSQL, countArgs, errBuild := buildCountSQL(params.tableName, params.queryParams)
				if errBuild != nil {
					return fmt.Errorf("构建COUNT查询失败: %w", errBuild)
				}
				var localCount int64
				if err := currentDB.QueryRowContext(countCtx, countSQL, countArgs...).Scan(&localCount); err != nil {
					slog.Warn("[DBManager Query] 计算总数时部分库查询失败", "error", err)
					return nil
				}
				atomic.AddInt64(&totalCount, localCount)
				return nil
			})
		}
		return countGroup.Wait()
	})

	// 并发获取数据
	g.Go(func() error {
		defer close(resultsChannel)
		dataGroup, dataCtx := errgroup.WithContext(queryCtx)
		sem := make(chan struct{}, runtime.NumCPU())

		for libName, instance := range dbInstances {
			m.mu.RLock()
			physicalSchemaInfo, hasPhysicalSchema := m.dbSchemaCache[instance.conn]
			m.mu.RUnlock()
			if !hasPhysicalSchema || physicalSchemaInfo == nil {
				continue
			}
			if _, tablePhysicallyExists := physicalSchemaInfo.allTablesAndColumns[params.tableName]; !tablePhysicallyExists {
				continue
			}

			currentLibName, currentDBConn := libName, instance.conn
			dataGroup.Go(func() error {
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-dataCtx.Done():
					return dataCtx.Err()
				}

				sqlQuery, queryArgs, errBuild := buildQuerySQL(params.tableName, params.selectFields, params.queryParams)
				if errBuild != nil {
					slog.Error("[DBManager Query] 构建SQL失败，已跳过此库", "error", errBuild)
					return nil
				}

				rows, errExec := currentDBConn.QueryContext(dataCtx, sqlQuery, queryArgs...)
				if errExec != nil {
					return fmt.Errorf("查询库 '%s/%s' 表 '%s' 失败: %w", bizName, currentLibName, params.tableName, errExec)
				}
				defer rows.Close()

				actualReturnedColumns, err := rows.Columns()
				if err != nil {
					return fmt.Errorf("获取列名失败: %w", err)
				}

				var libResults []map[string]any
				for rows.Next() {
					scanDest := make([]any, len(actualReturnedColumns))
					scanDestPtrs := make([]any, len(actualReturnedColumns))
					for i := range scanDest {
						scanDestPtrs[i] = &scanDest[i]
					}
					if err := rows.Scan(scanDestPtrs...); err != nil {
						slog.Warn("[DBManager Query] 扫描库行数据失败，跳过此行", "biz", bizName, "lib", currentLibName, "error", err)
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

				if err := rows.Err(); err != nil {
					return fmt.Errorf("迭代库 '%s/%s' 表 '%s' 行数据时发生错误: %w", bizName, currentLibName, params.tableName, err)
				}

				if len(libResults) > 0 {
					resultsChannel <- libResults
				}
				return nil
			})
		}
		return dataGroup.Wait()
	})

	// 聚合所有结果
	var allAggregatedResults []map[string]any
	for resSlice := range resultsChannel {
		allAggregatedResults = append(allAggregatedResults, resSlice...)
	}

	if err := g.Wait(); err != nil {
		slog.Error("[DBManager Query] 查询中发生错误", "biz", bizName, "table", params.tableName, "error", err)
		return allAggregatedResults, totalCount, fmt.Errorf("查询业务 '%s' 的表 '%s' 时发生部分错误: %w", bizName, params.tableName, err)
	}

	// 返回全部聚合结果，分页由调用方(queryInternal)处理
	return allAggregatedResults, totalCount, nil
}
