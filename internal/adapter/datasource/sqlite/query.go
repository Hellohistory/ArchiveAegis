// file: internal/adapter/datasource/sqlite/query.go
package sqlite

import (
	"ArchiveAegis/internal/core/port"
	"context"
	"fmt"
	"log"
	"runtime"
	"sort"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// Query 是实现 port.DataSource 接口的公开方法。
// 它的职责是接收标准的 QueryRequest，调用内部实现，并封装返回标准 QueryResult。
func (m *Manager) Query(ctx context.Context, req port.QueryRequest) (*port.QueryResult, error) {
	// 1. 调用重构后的内部核心实现，同时获取数据和总数
	results, total, err := m.queryInternal(ctx, req)
	if err != nil {
		// 直接将内部错误向上传递。即使有部分数据，也把错误作为主要信息。
		return &port.QueryResult{Data: results, Total: total, Source: m.Type()}, err
	}

	// 2. 将内部结果封装成标准的、可被外部消费的 QueryResult 结构
	queryResult := &port.QueryResult{
		Data:   results,
		Total:  total,
		Source: m.Type(), // 标明数据源类型
	}

	return queryResult, nil
}

// queryInternal 是查询逻辑的内部核心实现。
func (m *Manager) queryInternal(ctx context.Context, req port.QueryRequest) ([]map[string]any, int64, error) {
	bizAdminConfig, err := m.configService.GetBizQueryConfig(ctx, req.BizName)
	if err != nil {
		return nil, 0, fmt.Errorf("业务 '%s' 查询配置不可用: %w", req.BizName, err)
	}
	if bizAdminConfig == nil {
		return nil, 0, port.ErrBizNotFound
	}
	if !bizAdminConfig.IsPubliclySearchable {
		return nil, 0, port.ErrPermissionDenied
	}

	targetTableName := req.TableName
	if targetTableName == "" {
		targetTableName = bizAdminConfig.DefaultQueryTable
	}
	if targetTableName == "" {
		return nil, 0, fmt.Errorf("业务 '%s' 未能确定查询目标表", req.BizName)
	}

	tableAdminConfig, tableConfigExists := bizAdminConfig.Tables[targetTableName]
	if !tableConfigExists {
		return nil, 0, port.ErrTableNotFoundInBiz
	}
	if !tableAdminConfig.IsSearchable {
		return nil, 0, port.ErrPermissionDenied
	}

	validatedQueryParams := make([]port.QueryParam, 0, len(req.QueryParams))
	for _, p := range req.QueryParams {
		fieldSetting, fieldExists := tableAdminConfig.Fields[p.Field]
		if !fieldExists || !fieldSetting.IsSearchable {
			return nil, 0, fmt.Errorf("字段 '%s' 无效或不可搜索", p.Field)
		}
		validatedQueryParams = append(validatedQueryParams, p)
	}

	var selectFieldsForSQL []string
	if len(req.FieldsToReturn) > 0 {
		for _, fieldName := range req.FieldsToReturn {
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
	dbInstancesInBiz, bizGroupExists := m.group[req.BizName]
	m.mu.RUnlock()
	if !bizGroupExists || len(dbInstancesInBiz) == 0 {
		return []map[string]any{}, 0, nil
	}

	// --- 并发查询数据 和 并发计算总数 ---
	var totalCount int64
	resultsChannel := make(chan []map[string]any, len(dbInstancesInBiz))
	g, queryCtx := errgroup.WithContext(ctx)

	// Goroutine 1: 并发计算精确的总数
	g.Go(func() error {
		countGroup, countCtx := errgroup.WithContext(queryCtx)
		for _, db := range dbInstancesInBiz {
			currentDB := db // a copy of the loop variable
			countGroup.Go(func() error {
				countSQL, countArgs, errBuild := buildCountSQL(targetTableName, validatedQueryParams)
				if errBuild != nil {
					return fmt.Errorf("构建COUNT查询失败: %w", errBuild)
				}
				var localCount int64
				errScan := currentDB.QueryRowContext(countCtx, countSQL, countArgs...).Scan(&localCount)
				if errScan != nil {
					log.Printf("WARN: [DBManager Query] 计算总数时部分库查询失败 (不影响总结果): %v", errScan)
					return nil // Don't fail the entire group, just log it.
				}
				atomic.AddInt64(&totalCount, localCount)
				return nil
			})
		}
		return countGroup.Wait()
	})

	// Goroutine 2: 并发查询分页数据
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

				sqlQuery, queryArgs, errBuild := buildQuerySQL(targetTableName, selectFieldsForSQL, validatedQueryParams, req.Page, req.Size)
				if errBuild != nil {
					log.Printf("ERROR: [DBManager Query] 构建SQL失败，已跳过此库: %v", errBuild)
					return nil
				}

				rows, errExec := currentDBConn.QueryContext(dataCtx, sqlQuery, queryArgs...)
				if errExec != nil {
					return fmt.Errorf("查询库 '%s/%s' 表 '%s' 失败: %w", req.BizName, currentLibName, targetTableName, errExec)
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
						log.Printf("WARN: [DBManager Query] 扫描库 '%s/%s' 行数据失败: %v。跳过此行。", req.BizName, currentLibName, errScan)
						continue
					}
					// ✅ FIX: Corrected map initialization syntax
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
					return fmt.Errorf("迭代库 '%s/%s' 表 '%s' 行数据时发生错误: %w", req.BizName, currentLibName, targetTableName, errRows)
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
		log.Printf("错误: [DBManager Query] 业务 '%s' 表 '%s' 查询中发生错误: %v", req.BizName, targetTableName, err)
		return allAggregatedResults, totalCount, fmt.Errorf("查询业务 '%s' 的表 '%s' 时发生部分错误: %w", req.BizName, targetTableName, err)
	}

	return allAggregatedResults, totalCount, nil
}
