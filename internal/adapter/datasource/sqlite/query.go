// Package sqlite file: internal/adapter/datasource/sqlite/query.go
package sqlite

import (
	"ArchiveAegis/internal/core/port"
	"context"
	"fmt"
	"log/slog" // 使用 slog
	"runtime"
	"sort"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

type queryParam struct {
	Field string
	Value string
	Logic string
	Fuzzy bool
}

// Query 是适配新协议的公开方法。
// 它的职责是：解析和校验通用的查询请求，然后调用内部核心逻辑，最后将结果包装成通用格式返回。
func (m *Manager) Query(ctx context.Context, req port.QueryRequest) (*port.QueryResult, error) {
	queryMap := req.Query
	tableName, ok := queryMap["table"].(string)
	if !ok || tableName == "" {
		return nil, fmt.Errorf("无效请求: query 体必须包含一个有效的 'table' 字符串字段")
	}

	type parsedArgs struct {
		tableName      string
		queryParams    []queryParam // ✅ 使用包内私有的 queryParam
		fieldsToReturn []string
		page           int
		size           int
	}
	args := parsedArgs{
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
				return nil, fmt.Errorf("无效请求: filters 数组的第 %d 个元素不是一个有效的JSON对象", i)
			}

			param := queryParam{} // ✅ 使用包内私有的 queryParam
			if param.Field, ok = filterMap["field"].(string); !ok || param.Field == "" {
				return nil, fmt.Errorf("无效请求: filter 对象缺少或 'field' 字段类型不正确")
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
		return nil, err
	}

	return &port.QueryResult{
		Data: map[string]interface{}{
			"items": results,
			"total": total,
		},
		Source: m.Type(),
	}, nil
}

// queryInternal 是查询逻辑的内部核心实现。
// 它的函数签名被修改，以直接接收解析和验证过的参数，职责更单一。
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

				// ✅ 修正点 2: 使用 args 中的变量，而不是不存在的 req
				sqlQuery, queryArgs, errBuild := buildQuerySQL(targetTableName, selectFieldsForSQL, validatedQueryParams, args.page, args.size)
				if errBuild != nil {
					slog.Error("[DBManager Query] 构建SQL失败，已跳过此库", "error", errBuild)
					return nil
				}

				rows, errExec := currentDBConn.QueryContext(dataCtx, sqlQuery, queryArgs...)
				if errExec != nil {
					// ✅ 修正点 2: 使用 bizName 变量
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
						// ✅ 修正点 2: 使用 bizName 变量
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
					// ✅ 修正点 2: 使用 bizName 变量
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
