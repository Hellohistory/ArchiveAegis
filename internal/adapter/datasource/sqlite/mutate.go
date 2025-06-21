// Package sqlite file: internal/adapter/datasource/sqlite/mutate.go
package sqlite

import (
	"ArchiveAegis/internal/core/port"
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// Mutate 实现 port.DataSource 接口，处理通用的 CUD (Create, Update, Delete) 操作。
func (m *Manager) Mutate(ctx context.Context, req port.MutateRequest) (*port.MutateResult, error) {
	// 1. --- 获取业务和权限配置 ---
	bizAdminConfig, err := m.configService.GetBizQueryConfig(ctx, req.BizName)
	if err != nil {
		return nil, fmt.Errorf("业务 '%s' 查询配置不可用: %w", req.BizName, err)
	}
	if bizAdminConfig == nil {
		return nil, port.ErrBizNotFound
	}

	// 2. --- 严格地从通用的 Payload Map 中解析字段 ---
	payload := req.Payload
	tableName, ok := payload["table_name"].(string)
	if !ok || tableName == "" {
		return nil, errors.New("写操作的 payload 中必须包含一个有效的 'table_name' 字符串字段")
	}

	tableConfig, exists := bizAdminConfig.Tables[tableName]
	if !exists {
		return nil, port.ErrTableNotFoundInBiz
	}

	var opAllowed bool
	var sqlStmt string
	var args []interface{}

	// 3. --- 根据 operation 字符串决定执行何种操作 ---
	switch req.Operation {
	case "create":
		opAllowed = tableConfig.AllowCreate
		if opAllowed {
			data, ok := payload["data"].(map[string]interface{})
			if !ok {
				return nil, errors.New("create 操作的 payload 中必须包含一个有效的 'data' 对象")
			}
			sqlStmt, args, err = buildInsertSQL(tableName, data)
		}

	case "update":
		opAllowed = tableConfig.AllowUpdate
		if opAllowed {
			data, ok := payload["data"].(map[string]interface{})
			if !ok {
				return nil, errors.New("update 操作的 payload 中必须包含一个有效的 'data' 对象")
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
		return nil, fmt.Errorf("不支持的写操作类型: '%s'", req.Operation)
	}

	if !opAllowed {
		return nil, port.ErrPermissionDenied
	}
	if err != nil {
		return nil, fmt.Errorf("构建写操作SQL失败: %w", err)
	}

	// 4. --- 在所有相关数据库上顺序执行写操作 (快速失败) ---
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
			errMsg := fmt.Errorf("操作在库 '%s' 上失败并已中止。此前的写操作可能已成功，导致业务组数据不一致。错误: %w", libName, execErr)
			slog.Error("[DBManager Mutate]", "error", errMsg)
			return nil, errMsg
		}
		rowsAffected, _ := res.RowsAffected()
		totalRowsAffected += rowsAffected
	}

	// 5. --- 返回通用的 map 结果 ---
	return &port.MutateResult{
		Data: map[string]interface{}{
			"success":       true,
			"rows_affected": totalRowsAffected,
			"message":       "操作成功在所有相关库上执行。",
		},
		Source: m.Type(),
	}, nil
}

// ✅ NEW: 新增一个辅助函数，专门用于从 payload 中解析 filters，使代码更清晰
func parseFiltersFromPayload(payload map[string]interface{}) ([]port.QueryParam, error) {
	var filters []port.QueryParam

	rawFilters, ok := payload["filters"].([]interface{})
	if !ok {
		// 对于 update 和 delete，没有 filters 是危险的，可以根据业务需求决定是否报错
		// 在 buildDeleteSQL 中已经有保护，这里可以允许为空
		return filters, nil
	}

	for i, f := range rawFilters {
		filterMap, ok := f.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("无效请求: filters 数组的第 %d 个元素不是一个有效的JSON对象", i)
		}

		param := port.QueryParam{}
		if param.Field, ok = filterMap["field"].(string); !ok || param.Field == "" {
			return nil, fmt.Errorf("无效请求: filter 对象缺少或 'field' 字段类型不正确")
		}
		// value 可以是任何类型，我们统一按字符串处理
		param.Value = fmt.Sprintf("%v", filterMap["value"])
		param.Logic, _ = filterMap["logic"].(string)
		param.Fuzzy, _ = filterMap["fuzzy"].(bool)
		filters = append(filters, param)
	}

	return filters, nil
}
