// Package sqlite file: internal/adapter/datasource/sqlite/mutate.go
package sqlite

import (
	"ArchiveAegis/internal/core/port"
	"context"
	"errors"
	"fmt"
	"log"
)

// Mutate 实现 port.DataSource 接口，处理 CUD (Create, Update, Delete) 操作。
// [REFACTOR] 为了数据的可预见性，此方法已从并发执行改为顺序执行。
// 如果在一个业务组的多个库上执行写操作时，任何一个库失败，整个流程会立即停止并返回错误。
// 这不能保证跨多个库的原子性（已成功的写操作不会回滚），但能防止在出错后继续执行，并向调用者明确报告了不一致的风险。
func (m *Manager) Mutate(ctx context.Context, req port.MutateRequest) (*port.MutateResult, error) {
	bizAdminConfig, err := m.configService.GetBizQueryConfig(ctx, req.BizName)
	if err != nil {
		return nil, fmt.Errorf("业务 '%s' 查询配置不可用: %w", req.BizName, err)
	}
	if bizAdminConfig == nil {
		return nil, port.ErrBizNotFound
	}

	var opTableName string
	var opAllowed bool
	var sqlStmt string
	var args []interface{}

	switch {
	case req.CreateOp != nil:
		opTableName = req.CreateOp.TableName
		tableConfig, exists := bizAdminConfig.Tables[opTableName]
		if !exists {
			return nil, port.ErrTableNotFoundInBiz
		}
		opAllowed = tableConfig.AllowCreate
		if opAllowed {
			sqlStmt, args, err = buildInsertSQL(opTableName, req.CreateOp.Data)
		}

	case req.UpdateOp != nil:
		opTableName = req.UpdateOp.TableName
		tableConfig, exists := bizAdminConfig.Tables[opTableName]
		if !exists {
			return nil, port.ErrTableNotFoundInBiz
		}
		opAllowed = tableConfig.AllowUpdate
		if opAllowed {
			sqlStmt, args, err = buildUpdateSQL(opTableName, req.UpdateOp.Data, req.UpdateOp.Filters)
		}

	case req.DeleteOp != nil:
		opTableName = req.DeleteOp.TableName
		tableConfig, exists := bizAdminConfig.Tables[opTableName]
		if !exists {
			return nil, port.ErrTableNotFoundInBiz
		}
		opAllowed = tableConfig.AllowDelete
		if opAllowed {
			sqlStmt, args, err = buildDeleteSQL(opTableName, req.DeleteOp.Filters)
		}

	default:
		return nil, errors.New("无效的Mutate请求：缺少具体操作 (Create/Update/Delete)")
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
	// 按顺序在所有相关的数据库上执行写操作
	for libName, db := range dbInstances {
		res, execErr := db.ExecContext(ctx, sqlStmt, args...)
		if execErr != nil {
			// 快速失败：一旦有错误发生，立即中止并返回一个明确的错误信息。
			errMsg := fmt.Errorf("操作在库 '%s' 上失败并已中止。此前的写操作可能已成功，导致业务组数据不一致。错误: %w", libName, execErr)
			log.Printf("ERROR: [DBManager Mutate] %s", errMsg)
			return nil, errMsg
		}
		rowsAffected, _ := res.RowsAffected()
		totalRowsAffected += rowsAffected
	}

	return &port.MutateResult{
		Success:      true,
		RowsAffected: totalRowsAffected,
		Message:      "操作成功在所有相关库上执行。",
	}, nil
}
