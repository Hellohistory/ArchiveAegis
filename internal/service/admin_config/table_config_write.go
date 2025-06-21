// Package admin_config internal/service/admin_config/table_config_write.go
package admin_config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"ArchiveAegis/internal/core/domain"
)

// UpdateTableWritePermissions 更新指定表的写权限设置。
// 该方法会检查业务组是否存在，然后更新或插入表的写权限。
func (s *AdminConfigServiceImpl) UpdateTableWritePermissions(ctx context.Context, bizName, tableName string, perms domain.TableConfig) (err error) {
	if bizName == "" || tableName == "" {
		return fmt.Errorf("业务名和表名不能为空")
	}

	// 开启事务
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("严重错误: UpdateTableWritePermissions panic，事务已回滚 (业务 '%s', 表 '%s'): %v", bizName, tableName, p)
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
			log.Printf("警告: UpdateTableWritePermissions 执行失败，事务已回滚 (业务 '%s', 表 '%s'): %v", bizName, tableName, err)
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("提交事务失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, commitErr)
			}
		}
	}()

	// 检查业务组是否存在
	var exists bool
	checkQuery := "SELECT 1 FROM biz_overall_settings WHERE biz_name = ?"
	if err = tx.QueryRowContext(ctx, checkQuery, bizName).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("业务组 '%s' 不存在，无法设置表 '%s' 的权限", bizName, tableName)
		}
		return fmt.Errorf("检查业务组 '%s' 是否存在失败: %w", bizName, err)
	}

	// 获取当前 is_searchable 状态，若无记录则设为默认值 false。
	// 这样可以确保只更新写权限，而不影响 is_searchable 的状态。
	var isSearchable bool
	getSearchable := "SELECT is_searchable FROM biz_searchable_tables WHERE biz_name = ? AND table_name = ?"
	if errScan := tx.QueryRowContext(ctx, getSearchable, bizName, tableName).Scan(&isSearchable); errScan != nil {
		if errors.Is(errScan, sql.ErrNoRows) {
			isSearchable = false // 默认值
		} else {
			return fmt.Errorf("查询表 '%s/%s' 的 is_searchable 状态失败: %w", bizName, tableName, errScan)
		}
	}

	// UPSERT 权限信息：插入或更新表的写权限。
	upsertQuery := `
        INSERT INTO biz_searchable_tables 
        (biz_name, table_name, is_searchable, allow_create, allow_update, allow_delete)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT(biz_name, table_name) DO UPDATE SET
            allow_create = excluded.allow_create,
            allow_update = excluded.allow_update,
            allow_delete = excluded.allow_delete`
	if _, err = tx.ExecContext(ctx, upsertQuery,
		bizName, tableName, isSearchable, // 使用从数据库读取或默认的 isSearchable
		perms.AllowCreate, perms.AllowUpdate, perms.AllowDelete); err != nil {
		return fmt.Errorf("更新表 '%s/%s' 写权限失败: %w", bizName, tableName, err)
	}

	// 使相关缓存失效
	s.InvalidateCacheForBiz(bizName)
	log.Printf("信息: [AdminConfigService] 表 '%s/%s' 的写权限已更新，相关缓存已失效。", bizName, tableName)

	return nil // 事务提交由 defer 执行
}

// UpdateTableFieldSettings 全量更新指定表的字段配置。
// 该操作会删除现有配置，然后插入新的配置。
func (s *AdminConfigServiceImpl) UpdateTableFieldSettings(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) (err error) {
	if bizName == "" || tableName == "" {
		return fmt.Errorf("业务名或表名不能为空")
	}

	// 开启事务
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s', 表 '%s'): %w", bizName, tableName, err)
	}

	// 使用 defer 管理事务提交 / 回滚
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

	if len(fields) == 0 {
		// 如果没有字段配置，删除完即可，无需插入
		s.InvalidateCacheForBiz(bizName)
		return nil
	}

	// 准备批量插入字段配置的语句
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

	// 插入新字段配置
	for _, field := range fields {
		if _, err = stmt.ExecContext(ctx, bizName, tableName, field.FieldName,
			field.IsSearchable, field.IsReturnable, field.DataType); err != nil {
			return fmt.Errorf("插入字段配置失败 (业务 '%s', 表 '%s', 字段 '%s'): %w", bizName, tableName, field.FieldName, err)
		}
	}

	s.InvalidateCacheForBiz(bizName)
	return nil // 事务提交已在 defer 中处理
}
