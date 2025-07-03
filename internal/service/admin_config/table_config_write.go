// Package admin_config 提供对业务表权限和字段配置的管理服务。
// 该文件包含针对写权限和字段配置的更新操作，采用事务保证数据一致性，
// 并在更新后触发缓存失效以保持配置的实时性。
package admin_config

import (
	"ArchiveAegis/internal/core/domain"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
)

// UpdateTableWritePermissions 更新指定业务组下某张表的写权限设置。
//
// 参数：
//   - ctx: 上下文，用于管理请求生命周期与超时。
//   - bizName: 业务组名称，不能为空。
//   - tableName: 表名称，不能为空。
//   - perms: 待更新的写权限配置（是否允许创建、更新、删除）。
//
// 主要逻辑：
//  1. 参数校验，确保业务名和表名非空。
//  2. 开启数据库事务，保证整个操作的原子性。
//  3. 校验业务组是否存在，若不存在则返回错误。
//  4. 查询当前表的 is_searchable 状态，若无记录则使用默认值 false，
//     保证只更新写权限不影响可搜索配置。
//  5. 使用 UPSERT（SQLite 风格 ON CONFLICT）插入或更新写权限，
//     且显式保留原有 is_searchable 值，防止被默认值覆盖。
//  6. 提交事务前通过 defer 统一处理 panic、错误回滚和事务提交。
//  7. 在事务提交成功后触发缓存失效，确保服务下次读取到最新配置。
//
// 返回值：
//   - error: 操作过程中发生的错误，包含上下文信息。
func (s *AdminConfigServiceImpl) UpdateTableWritePermissions(ctx context.Context, bizName, tableName string, perms domain.TableConfig) (err error) {
	if bizName == "" || tableName == "" {
		return fmt.Errorf("业务名和表名不能为空")
	}

	// 开启事务，所有数据库操作在同一事务中执行
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, err)
	}

	// defer 统一处理事务的提交和回滚
	defer func() {
		if p := recover(); p != nil {
			// 捕获 panic 并回滚事务
			_ = tx.Rollback()
			log.Printf("严重错误: UpdateTableWritePermissions panic，事务已回滚 (业务 '%s', 表 '%s'): %v", bizName, tableName, p)
			panic(p)
		} else if err != nil {
			// 出现错误时回滚事务
			_ = tx.Rollback()
			log.Printf("警告: UpdateTableWritePermissions 执行失败，事务已回滚 (业务 '%s', 表 '%s'): %v", bizName, tableName, err)
		} else {
			// 无错误时提交事务
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("提交事务失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, commitErr)
			}
		}
	}()

	// 校验业务组是否存在
	var exists bool
	checkQuery := "SELECT 1 FROM biz_overall_settings WHERE biz_name = ?"
	if err = tx.QueryRowContext(ctx, checkQuery, bizName).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("业务组 '%s' 不存在，无法设置表 '%s' 的权限", bizName, tableName)
		}
		return fmt.Errorf("检查业务组 '%s' 是否存在失败: %w", bizName, err)
	}

	// 获取当前 is_searchable 状态，若不存在则使用默认 false
	var isSearchable bool
	getSearchable := "SELECT is_searchable FROM biz_searchable_tables WHERE biz_name = ? AND table_name = ?"
	if errScan := tx.QueryRowContext(ctx, getSearchable, bizName, tableName).Scan(&isSearchable); errScan != nil {
		if errors.Is(errScan, sql.ErrNoRows) {
			isSearchable = false
		} else {
			return fmt.Errorf("查询表 '%s/%s' 的 is_searchable 状态失败: %w", bizName, tableName, errScan)
		}
	}

	// 插入或更新写权限，保留原有 is_searchable 值
	upsertQuery := `
        INSERT INTO biz_searchable_tables
        (biz_name, table_name, is_searchable, allow_create, allow_update, allow_delete)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT(biz_name, table_name) DO UPDATE SET
            allow_create = excluded.allow_create,
            allow_update = excluded.allow_update,
            allow_delete = excluded.allow_delete,
            is_searchable = excluded.is_searchable;`
	if _, err = tx.ExecContext(ctx, upsertQuery,
		bizName, tableName, isSearchable,
		perms.AllowCreate, perms.AllowUpdate, perms.AllowDelete); err != nil {
		return fmt.Errorf("更新表 '%s/%s' 写权限失败: %w", bizName, tableName, err)
	}

	// 写权限更新后失效相关缓存
	s.InvalidateCacheForBiz(bizName)
	log.Printf("信息: [AdminConfigService] 表 '%s/%s' 的写权限已更新，相关缓存已失效。", bizName, tableName)

	return nil
}

// UpdateTableFieldSettings 全量更新指定业务组下某张表的字段配置。
//
// 整体流程：
//  1. 参数校验，业务名和表名不能为空。
//  2. 开启事务，保证删除和批量插入操作的一致性。
//  3. 删除原有字段配置。
//  4. 若 fields 列表为空，则直接失效缓存并返回，无需插入。
//  5. 使用预编译语句批量插入新的字段配置，包括字段名、可搜索、可返回及类型信息。
//  6. 在 defer 中统一处理 rollback 和 commit，并在提交后失效缓存。
//
// 参数：
//   - ctx: 上下文，用于管理请求生命周期。
//   - bizName: 业务组名称，不能为空。
//   - tableName: 表名称，不能为空。
//   - fields: 字段配置列表，包含 FieldName、IsSearchable、IsReturnable、DataType。
//
// 返回：
//   - error: 操作过程中发生的错误。
func (s *AdminConfigServiceImpl) UpdateTableFieldSettings(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) (err error) {
	if bizName == "" || tableName == "" {
		return fmt.Errorf("业务名或表名不能为空")
	}

	// 开启事务，确保删除与插入操作的一致性
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, err)
	}

	// defer 统一处理事务提交与回滚
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("严重错误: UpdateTableFieldSettings 触发 panic，事务已回滚 (业务 '%s', 表 '%s'): %v", bizName, tableName, p)
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
			log.Printf("警告: UpdateTableFieldSettings 执行失败，事务已回滚 (业务 '%s', 表 '%s'): %v", bizName, tableName, err)
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("提交事务失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, commitErr)
			}
		}
	}()

	// 删除旧字段配置
	if _, err = tx.ExecContext(ctx,
		"DELETE FROM biz_table_field_settings WHERE biz_name = ? AND table_name = ?", bizName, tableName); err != nil {
		return fmt.Errorf("清除旧字段配置失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, err)
	}

	// 如果没有新的字段配置列表，直接失效缓存并返回
	if len(fields) == 0 {
		s.InvalidateCacheForBiz(bizName)
		return nil
	}

	// 准备批量插入语句，提高插入效率
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO biz_table_field_settings 
		(biz_name, table_name, field_name, is_searchable, is_returnable, data_type) 
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("准备插入字段配置失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, err)
	}
	defer func() {
		if errClose := stmt.Close(); errClose != nil {
			log.Printf("警告: 关闭字段插入语句失败 (业务 '%s', 表 '%s'): %v", bizName, tableName, errClose)
		}
	}()

	// 批量插入新的字段配置
	for _, field := range fields {
		if _, err = stmt.ExecContext(ctx, bizName, tableName, field.FieldName,
			field.IsSearchable, field.IsReturnable, field.DataType); err != nil {
			return fmt.Errorf("插入字段配置失败 (业务 '%s', 表 '%s', 字段 '%s'): %w",
				bizName, tableName, field.FieldName, err)
		}
	}

	// 字段配置更新后失效缓存
	s.InvalidateCacheForBiz(bizName)
	return nil
}
