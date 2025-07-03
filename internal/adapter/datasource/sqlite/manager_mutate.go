// Package sqlite 提供 SQLite 数据源适配器的写操作管理功能。
// file: internal/adapter/datasource/sqlite/manager_mutate.go
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// handleDataMutate 解析并处理数据变更请求，执行内部写操作并返回写操作结果或错误。
// ctx: 上下文对象；req: 包含业务名称和写请求负载的请求信封。
func (m *Manager) handleDataMutate(ctx context.Context, req *v1.RequestEnvelope) (proto.Message, error) {
	// 将请求负载解包为 DataMutateRequest
	var mutateReq v1.DataMutateRequest
	if err := req.Payload.UnmarshalTo(&mutateReq); err != nil {
		// 解包失败时返回参数错误
		return nil, status.Errorf(codes.InvalidArgument, "解包 DataMutateRequest 失败: %v", err)
	}

	// 调用内部方法执行写操作
	goResult, err := m.mutateInternal(ctx, port.MutateRequest{
		BizName:   req.BizName,
		Operation: mutateReq.Operation,
		Payload:   mutateReq.GetPayload().AsMap(),
	})
	if err != nil {
		// 写操作执行失败，返回内部错误
		return nil, status.Errorf(codes.Internal, "写操作执行失败: %v", err)
	}

	// 将写操作结果转换为结构化的 structpb.Struct
	resultData, err := structpb.NewStruct(goResult.Data)
	if err != nil {
		// 序列化结果失败，返回内部错误
		return nil, status.Errorf(codes.Internal, "序列化写操作结果失败: %v", err)
	}
	// 构造并返回 DataMutateResult
	return &v1.DataMutateResult{Data: resultData}, nil
}

// mutateInternal 获取业务配置，构建并执行 SQL 写操作，返回执行结果或错误。
// ctx: 上下文对象；req: 包含业务名称、操作类型及载荷的写操作请求。
func (m *Manager) mutateInternal(ctx context.Context, req port.MutateRequest) (*port.MutateResult, error) {
	// 获取业务查询配置
	bizAdminConfig, err := m.configService.GetBizQueryConfig(ctx, req.BizName)
	if err != nil {
		return nil, fmt.Errorf("业务 '%s' 查询配置不可用: %w", req.BizName, err)
	}
	if bizAdminConfig == nil {
		// 业务配置不存在
		return nil, port.ErrBizNotFound
	}

	// 验证 payload 中的 table_name 字段
	payload := req.Payload
	tableName, ok := payload["table_name"].(string)
	if !ok || tableName == "" {
		return nil, status.Error(codes.InvalidArgument, "写操作的 payload 中必须包含一个有效的 'table_name' 字符串字段")
	}

	// 获取指定表的配置
	tableConfig, exists := bizAdminConfig.Tables[tableName]
	if !exists {
		return nil, port.ErrTableNotFoundInBiz
	}

	var opAllowed bool
	var sqlStmt string
	var args []interface{}

	// 根据操作类型构建对应的 SQL 语句和参数
	switch req.Operation {
	case "create":
		opAllowed = tableConfig.AllowCreate
		if opAllowed {
			// 解析 data 字段并构建插入语句
			data, ok := payload["data"].(map[string]interface{})
			if !ok {
				return nil, status.Error(codes.InvalidArgument, "create 操作的 payload 中必须包含一个有效的 'data' 对象")
			}
			sqlStmt, args, err = buildInsertSQL(tableName, data)
		}
	case "update":
		opAllowed = tableConfig.AllowUpdate
		if opAllowed {
			// 解析 data 和 filters 字段并构建更新语句
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
			// 解析 filters 字段并构建删除语句
			filters, parseErr := parseFiltersFromPayload(payload)
			if parseErr != nil {
				return nil, parseErr
			}
			sqlStmt, args, err = buildDeleteSQL(tableName, filters)
		}
	default:
		// 不支持的操作类型
		return nil, status.Errorf(codes.Unimplemented, "不支持的写操作类型: '%s'", req.Operation)
	}

	if !opAllowed {
		// 操作未授权
		return nil, port.ErrPermissionDenied
	}
	if err != nil {
		// 构建 SQL 失败
		return nil, fmt.Errorf("构建写操作SQL失败: %w", err)
	}

	// 获取业务对应的数据库实例的集合
	m.mu.RLock()
	dbInstances, bizExists := m.group[req.BizName]
	m.mu.RUnlock()
	if !bizExists {
		return nil, port.ErrBizNotFound
	}

	// 在所有相关库上执行 SQL 并累积受影响的行数
	var totalRowsAffected int64
	for libName, instance := range dbInstances {
		res, execErr := instance.conn.ExecContext(ctx, sqlStmt, args...)
		if execErr != nil {
			// 执行失败时记录错误并返回
			errMsg := fmt.Errorf("操作在库 '%s' 上失败并已中止。错误: %w", libName, execErr)
			slog.Error("[DBManager Mutate]", "error", errMsg)
			return nil, errMsg
		}
		rowsAffected, _ := res.RowsAffected()
		totalRowsAffected += rowsAffected
	}

	// 构造并返回写操作结果
	return &port.MutateResult{
		Data: map[string]interface{}{
			"success":       true,
			"rows_affected": totalRowsAffected,
			"message":       "操作成功在所有相关库上执行。",
		},
		Source: m.Type(),
	}, nil
}

// parseFiltersFromPayload 从写操作的 payload 中解析 filters 字段，返回查询参数列表并处理格式校验。
// payload: 原始请求载荷；返回值 filters: 解析后的查询条件列表。
func parseFiltersFromPayload(payload map[string]interface{}) ([]queryParam, error) {
	var filters []queryParam
	rawFilters, ok := payload["filters"].([]interface{})
	if !ok {
		// 当 filters 字段缺失或格式不正确时，视为空过滤条件
		return filters, nil
	}
	for i, f := range rawFilters {
		filterMap, ok := f.(map[string]interface{})
		if !ok {
			// 当 filters 数组元素格式不正确时返回错误
			return nil, status.Errorf(codes.InvalidArgument, "无效请求: filters 数组的第 %d 个元素不是一个有效的JSON对象", i)
		}
		var param queryParam
		// 验证 field 字段
		if param.Field, ok = filterMap["field"].(string); !ok || param.Field == "" {
			return nil, status.Error(codes.InvalidArgument, "无效请求: filter 对象缺少或 'field' 字段类型不正确")
		}
		// 设置 value 字段
		if val, exists := filterMap["value"]; exists {
			param.Value = fmt.Sprintf("%v", val)
		}
		// 设置逻辑与模糊匹配标志
		param.Logic, _ = filterMap["logic"].(string)
		param.Fuzzy, _ = filterMap["fuzzy"].(bool)
		filters = append(filters, param)
	}
	return filters, nil
}
