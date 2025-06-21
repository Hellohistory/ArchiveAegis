// Package admin_config internal/service/admin_config/biz_config_write.go
package admin_config

import (
	"context"
	"fmt"
	"log"
	"strings"

	"ArchiveAegis/internal/core/domain"
)

// UpdateBizOverallSettings 更新业务组的总体设置。
// settings 中的 nil 字段表示不更新该设置。
func (s *AdminConfigServiceImpl) UpdateBizOverallSettings(ctx context.Context, bizName string, settings domain.BizOverallSettings) (err error) {
	if bizName == "" {
		return fmt.Errorf("业务组名称不能为空")
	}

	// 开启事务
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s'): %w", bizName, err)
	}

	// 管理事务回滚/提交逻辑
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

	// 构建 UPDATE 语句及参数
	var updates []string
	var args []interface{}

	if settings.IsPubliclySearchable != nil {
		updates = append(updates, "is_publicly_searchable = ?")
		args = append(args, *settings.IsPubliclySearchable)
	}
	if settings.DefaultQueryTable != nil {
		updates = append(updates, "default_query_table = ?")
		args = append(args, *settings.DefaultQueryTable)
	}

	if len(updates) == 0 {
		log.Printf("信息: 未传入可更新字段，跳过更新 (业务 '%s')", bizName)
		return nil
	}

	args = append(args, bizName) // WHERE 子句的参数
	query := fmt.Sprintf("UPDATE biz_overall_settings SET %s WHERE biz_name = ?", strings.Join(updates, ", "))

	res, execErr := tx.ExecContext(ctx, query, args...)
	if execErr != nil {
		return fmt.Errorf("更新业务 '%s' 的总体配置失败: %w", bizName, execErr)
	}

	rows, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("无法获取受影响行数 (业务 '%s'): %w", bizName, rowsErr)
	}
	if rows == 0 {
		return fmt.Errorf("业务组 '%s' 未找到或数据未变更", bizName)
	}

	// 清除缓存
	s.InvalidateCacheForBiz(bizName)
	return nil // 提交逻辑已在 defer 中处理
}

// UpdateBizSearchableTables 全量更新一个业务组下所有可搜索的表。
// 该操作会删除现有配置，然后插入新的配置。
func (s *AdminConfigServiceImpl) UpdateBizSearchableTables(ctx context.Context, bizName string, tableNames []string) (err error) {
	if bizName == "" {
		return fmt.Errorf("业务组名称不能为空")
	}

	// 开启事务
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s'): %w", bizName, err)
	}

	// 管理事务的回滚 / 提交行为
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

	// 删除旧配置
	if _, err = tx.ExecContext(ctx,
		"DELETE FROM biz_searchable_tables WHERE biz_name = ?", bizName); err != nil {
		return fmt.Errorf("清除旧可搜索表失败 (业务 '%s'): %w", bizName, err)
	}

	if len(tableNames) == 0 {
		// 如果没有传入新的表名，则只删除旧配置即可
		s.InvalidateCacheForBiz(bizName)
		return nil
	}

	// 准备插入新配置的语句
	// 注意：这里没有设置 is_searchable, allow_create, allow_update, allow_delete 的值
	// 因为这些是默认值，或者会在后续的 UpdateTableWritePermissions 和 UpdateTableFieldSettings 中设置。
	stmt, err := tx.PrepareContext(ctx,
		"INSERT INTO biz_searchable_tables (biz_name, table_name) VALUES (?, ?)")
	if err != nil {
		return fmt.Errorf("准备插入语句失败 (业务 '%s'): %w", bizName, err)
	}
	defer func() {
		if errClose := stmt.Close(); errClose != nil {
			log.Printf("警告: 关闭 stmt 失败 (业务 '%s'): %v", bizName, errClose)
		}
	}()

	// 插入新配置
	for _, tableName := range tableNames {
		if _, err = stmt.ExecContext(ctx, bizName, tableName); err != nil {
			return fmt.Errorf("插入可搜索表 '%s' 失败 (业务 '%s'): %w", tableName, bizName, err)
		}
	}

	s.InvalidateCacheForBiz(bizName)
	return nil // 事务提交由 defer 完成
}
