// Package admin_config internal/service/admin_config/biz_config_write.go
package admin_config

import (
	"ArchiveAegis/internal/core/domain"
	"context"
	"database/sql"
	"fmt"
	"log"
)

// UpdateBizOverallSettings 更新业务组的总体设置。
func (s *AdminConfigServiceImpl) UpdateBizOverallSettings(ctx context.Context, bizName string, settings domain.BizOverallSettings) (err error) {
	if bizName == "" {
		return fmt.Errorf("业务组名称不能为空")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s'): %w", bizName, err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("严重错误: UpdateBizOverallSettings 触发 panic，事务已回滚 (业务 '%s'): %v", bizName, p)
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
			log.Printf("警告: UpdateBizOverallSettings 执行失败，事务已回滚 (业务 '%s'): %v", bizName, err)
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("提交事务失败 (业务 '%s'): %w", bizName, commitErr)
			}
		}
	}()

	var isPubliclySearchable sql.NullBool
	if settings.IsPubliclySearchable != nil {
		isPubliclySearchable.Bool = *settings.IsPubliclySearchable
		isPubliclySearchable.Valid = true
	} else {
		isPubliclySearchable.Bool = true // 默认为 true
		isPubliclySearchable.Valid = true
	}

	var defaultQueryTable sql.NullString
	if settings.DefaultQueryTable != nil {
		defaultQueryTable.String = *settings.DefaultQueryTable
		defaultQueryTable.Valid = true
	}

	upsertQuery := `
        INSERT INTO biz_overall_settings (biz_name, is_publicly_searchable, default_query_table)
        VALUES (?, ?, ?)
        ON CONFLICT(biz_name) DO UPDATE SET
            is_publicly_searchable = excluded.is_publicly_searchable,
            default_query_table = excluded.default_query_table;`

	_, execErr := tx.ExecContext(ctx, upsertQuery,
		bizName, isPubliclySearchable, defaultQueryTable)
	if execErr != nil {
		return fmt.Errorf("更新/插入业务 '%s' 的总体配置失败: %w", bizName, execErr)
	}

	s.InvalidateCacheForBiz(bizName)
	log.Printf("信息: 业务组 '%s' 的总体配置已更新/插入，相关缓存已失效。", bizName)

	return nil
}

// UpdateBizSearchableTables 全量更新一个业务组下所有可搜索的表。
// 该操作会先将该业务组下所有表的 is_searchable 状态置为 false，然后再将传入列表中的表的状态置为 true。
func (s *AdminConfigServiceImpl) UpdateBizSearchableTables(ctx context.Context, bizName string, tableNames []string) (err error) {
	if bizName == "" {
		return fmt.Errorf("业务组名称不能为空")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s'): %w", bizName, err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("严重错误: UpdateBizSearchableTables 触发 panic，事务已回滚 (业务 '%s'): %v", bizName, p)
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
			log.Printf("警告: UpdateBizSearchableTables 执行失败，事务已回滚 (业务 '%s'): %v", bizName, err)
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("提交事务失败 (业务 '%s'): %w", bizName, commitErr)
			}
		}
	}()

	resetQuery := "UPDATE biz_searchable_tables SET is_searchable = FALSE WHERE biz_name = ?"
	if _, err = tx.ExecContext(ctx, resetQuery, bizName); err != nil {
		return fmt.Errorf("重置旧可搜索表状态失败 (业务 '%s'): %w", bizName, err)
	}

	if len(tableNames) == 0 {
		s.InvalidateCacheForBiz(bizName)
		return nil // 如果没有新表，重置后直接返回
	}

	// 如果表记录已存在，则只更新 is_searchable 字段；如果不存在，则插入新行，并将 is_searchable 设为 TRUE。
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO biz_searchable_tables (biz_name, table_name, is_searchable)
        VALUES (?, ?, TRUE)
        ON CONFLICT(biz_name, table_name) DO UPDATE SET
            is_searchable = TRUE;
    `)
	if err != nil {
		return fmt.Errorf("准备 UPSERT 语句失败 (业务 '%s'): %w", bizName, err)
	}
	defer func() {
		if errClose := stmt.Close(); errClose != nil {
			log.Printf("警告: 关闭 stmt 失败 (业务 '%s'): %v", bizName, errClose)
		}
	}()

	for _, tableName := range tableNames {
		if _, err = stmt.ExecContext(ctx, bizName, tableName); err != nil {
			return fmt.Errorf("UPSERT 可搜索表 '%s' 失败 (业务 '%s'): %w", tableName, bizName, err)
		}
	}

	s.InvalidateCacheForBiz(bizName)
	return nil
}
