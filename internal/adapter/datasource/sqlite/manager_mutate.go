// Package sqlite file: internal/adapter/datasource/sqlite/manager_mutate.go
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
	for libName, instance := range dbInstances {

		res, execErr := instance.conn.ExecContext(ctx, sqlStmt, args...)
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
		// 允许 filters 为空
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
