// Package sqlite 实现了内置的、基于文件的 SQLite 数据源执行器。
// file: internal/adapter/datasource/sqlite/manager_query.go

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
//  多路归并排序所需的数据结构 (k-way merge)
// =============================================================================

// heapItem 代表从一个数据库分片流中取出的一个数据项，用于多路归并。
type heapItem struct {
	Data        map[string]any // 查询到的一行数据。
	StreamIndex int            // 数据来源的数据库实例（分片）的索引。
}

// PriorityQueue 是一个实现了 heap.Interface 的最小堆，用于对多个分片返回的有序数据进行归并排序。
// 它通过指针接收器实现，以确保 Push 和 Pop 操作能修改切片头。
type PriorityQueue []*heapItem

// Len 返回队列中的元素数量。
func (pq *PriorityQueue) Len() int { return len(*pq) }

// Less 定义了元素的排序规则，这里基于 'id' 字段进行升序排序。
// 它能处理 int64, float64, string 类型的 'id'，并提供一个回退机制。
func (pq *PriorityQueue) Less(i, j int) bool {
	// 通过指针访问切片元素。
	valI, iExists := (*pq)[i].Data["id"]
	valJ, jExists := (*pq)[j].Data["id"]

	// 处理 'id' 字段不存在或为 nil 的情况。
	if !iExists || valI == nil {
		return false // 无有效值的元素被视为较大。
	}
	if !jExists || valJ == nil {
		return true // 另一个元素无有效值，则当前元素较小。
	}

	// 根据 'id' 的实际类型进行比较。
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
	// 如果类型不匹配或不是预设类型，则回退到字符串比较。
	return fmt.Sprintf("%v", valI) < fmt.Sprintf("%v", valJ)
}

// Swap 交换队列中两个元素的位置。
func (pq *PriorityQueue) Swap(i, j int) {
	(*pq)[i], (*pq)[j] = (*pq)[j], (*pq)[i]
}

// Push 向队列末尾添加一个元素。
func (pq *PriorityQueue) Push(x any) {
	*pq = append(*pq, x.(*heapItem))
}

// Pop 从队列末尾移除并返回一个元素。
func (pq *PriorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // 避免内存泄漏。
	*pq = old[0 : n-1]
	return item
}

// =============================================================================
//  查询逻辑实现
// =============================================================================

// queryParam 封装了一个独立的查询过滤条件。
type queryParam struct {
	Field string // 待过滤的字段名。
	Value string // 过滤条件的值。
	Logic string // 逻辑操作符，如 "AND", "OR"。
	Fuzzy bool   // 是否进行模糊匹配。
}

// rawQueryArgs 封装了从 gRPC 请求中直接解析出的、未经校验的原始查询参数。
type rawQueryArgs struct {
	tableName      string       // 目标表名。
	queryParams    []queryParam // 过滤条件列表。
	fieldsToReturn []string     // 希望返回的字段列表。
	page           int          // 分页：页码。
	size           int          // 分页：每页大小。
}

// validatedQueryParams 封装了通过权限和规则校验后的、可安全执行的查询参数。
type validatedQueryParams struct {
	tableName    string       // 最终确定的表名。
	queryParams  []queryParam // 经过校验的过滤条件。
	selectFields []string     // 经过权限过滤后，允许返回的字段。
	page         int          // 分页：页码。
	size         int          // 分页：每页大小。
}

// handleDataQuery 是数据查询请求的 gRPC 入口，负责解析请求、调用内部查询并封装返回结果。
// ctx: 上下文对象；req: 包含业务名称和查询负载的请求信封。
func (m *Manager) handleDataQuery(ctx context.Context, req *v1.RequestEnvelope) (proto.Message, error) {
	// 将请求负载解包为 DataQueryRequest。
	var queryReq v1.DataQueryRequest
	if err := req.Payload.UnmarshalTo(&queryReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 DataQueryRequest 失败: %v", err)
	}

	// 从 Protobuf Struct 中解析查询参数。
	queryMap := queryReq.GetQuery().AsMap()
	tableName, ok := queryMap["table"].(string)
	if !ok || tableName == "" {
		return nil, status.Error(codes.InvalidArgument, "无效请求: query 体必须包含一个有效的 'table' 字符串字段")
	}

	// 构造原始查询参数结构体，并设置默认分页值。
	args := rawQueryArgs{
		tableName: tableName,
		page:      1,
		size:      50,
	}

	// 解析分页、过滤条件和返回字段。
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

	// 调用核心查询逻辑。
	results, total, err := m.queryInternal(ctx, req.BizName, args)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "查询数据失败: %v", err)
	}

	// 将查询结果转换为 Protobuf 结构。
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

	// 封装并返回最终的 DataQueryResult。
	return &v1.DataQueryResult{Data: resultData}, nil
}

// queryInternal 是核心查询逻辑，它协调校验、并发查询、多路归并和内存分页。
// ctx: 上下文对象；bizName: 业务名称；args: 未经校验的原始查询参数。
func (m *Manager) queryInternal(ctx context.Context, bizName string, args rawQueryArgs) ([]map[string]any, int64, error) {
	// 步骤 1: 校验请求参数和权限。
	validatedParams, err := m.validateQueryRequest(ctx, bizName, args)
	if err != nil {
		return nil, 0, err
	}

	// 获取业务对应的数据库实例集合。
	m.mu.RLock()
	dbInstancesInBiz, bizGroupExists := m.group[bizName]
	m.mu.RUnlock()
	if !bizGroupExists || len(dbInstancesInBiz) == 0 {
		return []map[string]any{}, 0, nil // 如果业务不存在或没有库，直接返回空结果。
	}

	// 步骤 2: 并发查询所有分片，获取每个分片的有序结果切片和总数。
	sortedSlices, totalCount, err := m.executeConcurrentQuery(ctx, bizName, dbInstancesInBiz, validatedParams)
	if err != nil {
		return nil, 0, err
	}

	// 步骤 3: 使用最小堆进行多路归并。
	pq := make(PriorityQueue, 0, len(sortedSlices))
	heap.Init(&pq)

	// 将每个分片结果集的第一个元素推入最小堆。
	sliceCursors := make([]int, len(sortedSlices)) // 跟踪每个分片切片的当前处理位置。
	for i, slice := range sortedSlices {
		if len(slice) > 0 {
			heap.Push(&pq, &heapItem{Data: slice[0], StreamIndex: i})
			sliceCursors[i] = 1 // 指向下一个待处理元素。
		}
	}

	// 从堆中依次弹出最小元素，构建最终的、完全有序的结果集。
	allAggregatedResults := make([]map[string]any, 0, validatedParams.size)
	for pq.Len() > 0 {
		item := heap.Pop(&pq).(*heapItem)
		allAggregatedResults = append(allAggregatedResults, item.Data)

		// 从弹出元素的来源分片中，取下一个元素推入堆。
		streamIdx := item.StreamIndex
		if sliceCursors[streamIdx] < len(sortedSlices[streamIdx]) {
			nextItem := sortedSlices[streamIdx][sliceCursors[streamIdx]]
			heap.Push(&pq, &heapItem{Data: nextItem, StreamIndex: streamIdx})
			sliceCursors[streamIdx]++
		}
	}

	// 步骤 4: 在完全归并排序后的结果集上进行内存分页。
	start := (validatedParams.page - 1) * validatedParams.size
	if start < 0 || start > len(allAggregatedResults) {
		return []map[string]any{}, totalCount, nil // 页码超出范围，返回空数据。
	}
	end := start + validatedParams.size
	if end > len(allAggregatedResults) {
		end = len(allAggregatedResults)
	}

	return allAggregatedResults[start:end], totalCount, nil
}

// validateQueryRequest 负责所有查询前的权限和参数校验，是安全的第一道防线。
// ctx: 上下文对象；bizName: 业务名称；args: 原始查询参数。
func (m *Manager) validateQueryRequest(ctx context.Context, bizName string, args rawQueryArgs) (*validatedQueryParams, error) {
	// 获取业务查询配置。
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

	// 确定目标查询表，如果未指定则使用默认表。
	targetTableName := args.tableName
	if targetTableName == "" {
		targetTableName = bizAdminConfig.DefaultQueryTable
	}
	if targetTableName == "" {
		return nil, fmt.Errorf("业务 '%s' 未能确定查询目标表", bizName)
	}

	// 校验目标表是否存在及是否可被搜索。
	tableAdminConfig, tableConfigExists := bizAdminConfig.Tables[targetTableName]
	if !tableConfigExists {
		return nil, port.ErrTableNotFoundInBiz
	}
	if !tableAdminConfig.IsSearchable {
		return nil, port.ErrPermissionDenied
	}

	// 校验请求中的每个过滤字段是否被配置为可搜索。
	validatedParamsSlice := make([]queryParam, 0, len(args.queryParams))
	for _, p := range args.queryParams {
		fieldSetting, fieldExists := tableAdminConfig.Fields[p.Field]
		if !fieldExists || !fieldSetting.IsSearchable {
			return nil, fmt.Errorf("字段 '%s' 无效或不可搜索", p.Field)
		}
		validatedParamsSlice = append(validatedParamsSlice, p)
	}

	// 校验并构建最终的 SELECT 字段列表。
	var selectFieldsForSQL []string
	if len(args.fieldsToReturn) > 0 {
		// 如果用户指定了返回字段，则逐一校验权限。
		for _, fieldName := range args.fieldsToReturn {
			fieldSetting, fieldExists := tableAdminConfig.Fields[fieldName]
			if !fieldExists || !fieldSetting.IsReturnable {
				return nil, fmt.Errorf("安全策略冲突：字段 '%s' 未被授权返回", fieldName)
			}
			selectFieldsForSQL = append(selectFieldsForSQL, fieldName)
		}
	} else {
		// 如果用户未指定，则返回所有被配置为可返回的字段。
		for fieldName, fieldSetting := range tableAdminConfig.Fields {
			if fieldSetting.IsReturnable {
				selectFieldsForSQL = append(selectFieldsForSQL, fieldName)
			}
		}
	}

	if len(selectFieldsForSQL) == 0 {
		return nil, fmt.Errorf("在表 '%s' 的配置中，没有找到任何可供返回的字段", targetTableName)
	}
	sort.Strings(selectFieldsForSQL) // 排序以确保 SQL 缓存命中率。

	// 返回经过完全校验和处理的查询参数。
	return &validatedQueryParams{
		tableName:    targetTableName,
		queryParams:  validatedParamsSlice,
		selectFields: selectFieldsForSQL,
		page:         args.page,
		size:         args.size,
	}, nil
}

// executeConcurrentQuery 并发地向所有相关数据库分片发起查询，并收集结果。
// ctx: 上下文对象；bizName: 业务名称；dbInstances: 数据库实例映射；params: 校验后的查询参数。
// 返回值: 每个分片的结果切片组成的切片、所有分片的总行数、错误。
func (m *Manager) executeConcurrentQuery(ctx context.Context, bizName string, dbInstances map[string]*dbInstance, params *validatedQueryParams) ([][]map[string]any, int64, error) {
	var totalCount int64
	resultsChan := make(chan []map[string]any, len(dbInstances))
	g, queryCtx := errgroup.WithContext(ctx)

	// Goroutine 1: 并发计算所有分片的总行数。
	g.Go(func() error {
		countGroup, countCtx := errgroup.WithContext(queryCtx)
		for _, instance := range dbInstances {
			instance := instance // 为 goroutine 捕获循环变量。
			countGroup.Go(func() error {
				// 检查物理表是否存在于缓存中，避免无效查询。
				m.mu.RLock()
				physicalSchemaInfo, hasPhysicalSchema := m.dbSchemaCache[instance.conn]
				m.mu.RUnlock()
				if !hasPhysicalSchema || !physicalSchemaInfo.tableExists(params.tableName) {
					return nil
				}

				// 构建 COUNT SQL。
				countSQL, countArgs, err := buildCountSQL(params.tableName, params.queryParams)
				if err != nil {
					return err
				}

				// 执行 COUNT 查询并以原子方式累加总数。
				var localCount int64
				if err := instance.conn.QueryRowContext(countCtx, countSQL, countArgs...).Scan(&localCount); err != nil {
					slog.WarnContext(countCtx, "[DBManager Query] 计算总数时部分库查询失败", "error", err)
					return nil // 容忍单个分片 COUNT 失败，仅记录日志。
				}
				atomic.AddInt64(&totalCount, localCount)
				return nil
			})
		}
		return countGroup.Wait()
	})

	// Goroutine 2: 并发获取所有分片的数据行。
	g.Go(func() error {
		defer close(resultsChan)
		dataGroup, dataCtx := errgroup.WithContext(queryCtx)
		for libName, instance := range dbInstances {
			libName, instance := libName, instance // 为 goroutine 捕获循环变量。
			dataGroup.Go(func() error {
				// 同样检查物理表是否存在。
				m.mu.RLock()
				physicalSchemaInfo, hasPhysicalSchema := m.dbSchemaCache[instance.conn]
				m.mu.RUnlock()
				if !hasPhysicalSchema || !physicalSchemaInfo.tableExists(params.tableName) {
					return nil
				}

				// 构建 SELECT SQL。
				sqlQuery, queryArgs, err := buildQuerySQL(params.tableName, params.selectFields, params.queryParams)
				if err != nil {
					slog.ErrorContext(dataCtx, "[DBManager Query] 构建SQL失败", "error", err)
					return nil // 构建失败，跳过此分片。
				}

				// 执行 SELECT 查询。
				rows, err := instance.conn.QueryContext(dataCtx, sqlQuery, queryArgs...)
				if err != nil {
					return fmt.Errorf("查询库 '%s/%s' 表 '%s' 失败: %w", bizName, libName, params.tableName, err)
				}
				defer rows.Close()

				// 扫描所有行到 map 切片中。
				libResults, err := scanAllRowsToMap(rows, libName)
				if err != nil {
					return fmt.Errorf("扫描库 '%s/%s' 数据失败: %w", bizName, libName, err)
				}

				// 将非空结果集发送到通道。
				if len(libResults) > 0 {
					resultsChan <- libResults
				}
				return nil
			})
		}
		return dataGroup.Wait()
	})

	// 收集所有从通道传来的结果切片。
	var allSlices [][]map[string]any
	for slice := range resultsChan {
		allSlices = append(allSlices, slice)
	}

	// 等待所有 goroutine 完成，并检查是否有错误发生。
	if err := g.Wait(); err != nil {
		return nil, 0, err
	}

	return allSlices, totalCount, nil
}

// scanAllRowsToMap 是一个辅助函数，它扫描一个 *sql.Rows 中的所有行，
// 将每行转换为 map[string]any，并注入 '__lib' 字段以标识数据来源。
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
			scanArgs[i] = &values[i] // 创建指向每个值槽的指针。
		}

		// 将行数据扫描到指针切片中。
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}

		// 创建 map 并填充数据。
		rowData := make(map[string]any)
		rowData["__lib"] = libName // 注入来源库名称。
		for i, colName := range columns {
			// 将数据库返回的 []byte 类型转换为 string，便于 JSON 序列化。
			if bytes, ok := values[i].([]byte); ok {
				rowData[colName] = string(bytes)
			} else {
				rowData[colName] = values[i]
			}
		}
		results = append(results, rowData)
	}

	// 检查扫描循环中是否发生错误。
	return results, rows.Err()
}
