// file: internal/adapter/datasource/sqlite/manager_query.go

// Package sqlite 实现了内置的、基于文件的 SQLite 数据源执行器。
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"container/heap"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// =============================================================================
//  多路归并排序所需的数据结构 (k-way merge) - 接收器已统一
// =============================================================================

// heapItem 代表从一个数据库分片流中取出的一个数据项。
type heapItem struct {
	Data        map[string]any // 查询到的一行数据
	StreamIndex int            // 它来自哪个数据库实例（分片）的流
}

// PriorityQueue 是一个实现了 heap.Interface 的最小堆，用于多路归并。
type PriorityQueue []*heapItem

// Len 方法为指针接收器
func (pq *PriorityQueue) Len() int { return len(*pq) }

// Less 方法为指针接收器
func (pq *PriorityQueue) Less(i, j int) bool {
	// 内部逻辑保持不变，但通过指针访问切片元素
	valI, iExists := (*pq)[i].Data["id"]
	valJ, jExists := (*pq)[j].Data["id"]

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
	// Fallback
	return fmt.Sprintf("%v", valI) < fmt.Sprintf("%v", valJ)
}

// Swap 方法为指针接收器
func (pq *PriorityQueue) Swap(i, j int) {
	(*pq)[i], (*pq)[j] = (*pq)[j], (*pq)[i]
}

// Push 方法已经是正确的指针接收器
func (pq *PriorityQueue) Push(x any) {
	*pq = append(*pq, x.(*heapItem))
}

// Pop 方法已经是正确的指针接收器
func (pq *PriorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*pq = old[0 : n-1]
	return item
}

// =============================================================================
//  查询逻辑实现
// =============================================================================

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

// queryInternal 使用最终的、健壮的并发模型和多路归并算法。
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

	// 并发执行查询，获取每个分片的“已排序结果切片”和总数
	sortedSlices, totalCount, err := m.executeConcurrentQuery(ctx, bizName, dbInstancesInBiz, validatedParams)
	if err != nil {
		return nil, 0, err
	}

	// 初始化用于多路归并的最小堆
	pq := make(PriorityQueue, 0, len(sortedSlices))
	heap.Init(&pq)

	// 将每个“已排序切片”的第一个元素放入堆中
	sliceCursors := make([]int, len(sortedSlices)) // 记录每个切片的当前处理位置
	for i, slice := range sortedSlices {
		if len(slice) > 0 {
			heap.Push(&pq, &heapItem{Data: slice[0], StreamIndex: i})
			sliceCursors[i] = 1 // 指向下一个元素
		}
	}

	// 开始归并处理
	allAggregatedResults := make([]map[string]any, 0, validatedParams.size)
	for pq.Len() > 0 {
		item := heap.Pop(&pq).(*heapItem)
		allAggregatedResults = append(allAggregatedResults, item.Data)

		streamIdx := item.StreamIndex
		if sliceCursors[streamIdx] < len(sortedSlices[streamIdx]) {
			nextItem := sortedSlices[streamIdx][sliceCursors[streamIdx]]
			heap.Push(&pq, &heapItem{Data: nextItem, StreamIndex: streamIdx})
			sliceCursors[streamIdx]++
		}
	}

	// 在最终的、完全有序的结果上进行内存分页
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

// executeConcurrentQuery 使用简化的并发模型，返回“结果切片的切片”。
func (m *Manager) executeConcurrentQuery(ctx context.Context, bizName string, dbInstances map[string]*dbInstance, params *validatedQueryParams) ([][]map[string]any, int64, error) {
	var totalCount int64
	resultsChan := make(chan []map[string]any, len(dbInstances))
	g, queryCtx := errgroup.WithContext(ctx)

	// 并发计算总数 (逻辑不变，但确保了并发安全)
	g.Go(func() error {
		countGroup, countCtx := errgroup.WithContext(queryCtx)
		for _, instance := range dbInstances {
			instance := instance // 正确捕获循环变量
			countGroup.Go(func() error {
				m.mu.RLock()
				physicalSchemaInfo, hasPhysicalSchema := m.dbSchemaCache[instance.conn]
				m.mu.RUnlock()
				if !hasPhysicalSchema || !physicalSchemaInfo.tableExists(params.tableName) {
					return nil
				}

				countSQL, countArgs, err := buildCountSQL(params.tableName, params.queryParams)
				if err != nil {
					return err
				}

				var localCount int64
				if err := instance.conn.QueryRowContext(countCtx, countSQL, countArgs...).Scan(&localCount); err != nil {
					slog.WarnContext(countCtx, "[DBManager Query] 计算总数时部分库查询失败", "error", err)
					return nil
				}
				atomic.AddInt64(&totalCount, localCount)
				return nil
			})
		}
		return countGroup.Wait()
	})

	// 并发获取所有数据行
	g.Go(func() error {
		defer close(resultsChan)
		dataGroup, dataCtx := errgroup.WithContext(queryCtx)
		for libName, instance := range dbInstances {
			libName := libName // 正确捕获循环变量
			instance := instance

			dataGroup.Go(func() error {
				m.mu.RLock()
				physicalSchemaInfo, hasPhysicalSchema := m.dbSchemaCache[instance.conn]
				m.mu.RUnlock()
				if !hasPhysicalSchema || !physicalSchemaInfo.tableExists(params.tableName) {
					return nil
				}

				sqlQuery, queryArgs, err := buildQuerySQL(params.tableName, params.selectFields, params.queryParams)
				if err != nil {
					slog.ErrorContext(dataCtx, "[DBManager Query] 构建SQL失败", "error", err)
					return nil
				}

				rows, err := instance.conn.QueryContext(dataCtx, sqlQuery, queryArgs...)
				if err != nil {
					return fmt.Errorf("查询库 '%s/%s' 表 '%s' 失败: %w", bizName, libName, params.tableName, err)
				}
				defer rows.Close()

				libResults, err := scanAllRowsToMap(rows, libName)
				if err != nil {
					return fmt.Errorf("扫描库 '%s/%s' 数据失败: %w", bizName, libName, err)
				}

				if len(libResults) > 0 {
					resultsChan <- libResults
				}
				return nil
			})
		}
		return dataGroup.Wait()
	})

	var allSlices [][]map[string]any
	for slice := range resultsChan {
		allSlices = append(allSlices, slice)
	}

	if err := g.Wait(); err != nil {
		return nil, 0, err
	}

	return allSlices, totalCount, nil
}

// scanAllRowsToMap 是一个辅助函数，它扫描一个 *sql.Rows 中的所有行，并注入 __lib 字段。
func scanAllRowsToMap(rows *sql.Rows, libName string) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		scanArgs := make([]any, len(values))
		for i := range values {
			scanArgs[i] = &values[i]
		}

		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}

		rowData := make(map[string]any)
		rowData["__lib"] = libName
		for i, colName := range columns {
			if bytes, ok := values[i].([]byte); ok {
				rowData[colName] = string(bytes)
			} else {
				rowData[colName] = values[i]
			}
		}
		results = append(results, rowData)
	}

	return results, rows.Err()
}
