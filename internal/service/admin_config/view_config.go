// Package admin_config internal/service/admin_config/view_config.go
package admin_config

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"ArchiveAegis/internal/core/domain"
)

// GetDefaultViewConfig 从数据库获取指定表的默认视图配置。
func (s *AdminConfigServiceImpl) GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error) {
	if bizName == "" || tableName == "" {
		return nil, fmt.Errorf("业务组和表名不能为空")
	}

	var configJSON string
	query := `SELECT view_config_json FROM biz_view_definitions WHERE biz_name = ? AND table_name = ? AND is_default = TRUE LIMIT 1`

	err := s.db.QueryRowContext(ctx, query, bizName, tableName).Scan(&configJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // 非错误，仅未配置
	}
	if err != nil {
		return nil, fmt.Errorf("获取视图配置时发生数据库错误: %w", err)
	}

	var viewConf domain.ViewConfig
	if err := json.Unmarshal([]byte(configJSON), &viewConf); err != nil {
		return nil, fmt.Errorf("视图配置数据格式无效: %w", err)
	}

	return &viewConf, nil
}

// GetAllViewConfigsForBiz 从数据库获取指定业务组下所有表的全部视图配置。
func (s *AdminConfigServiceImpl) GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error) {
	if bizName == "" {
		return nil, fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	query := `SELECT table_name, view_config_json FROM biz_view_definitions WHERE biz_name = ?`
	rows, err := s.db.QueryContext(ctx, query, bizName)
	if err != nil {
		return nil, fmt.Errorf("获取业务 '%s' 的所有视图配置时发生数据库错误: %w", bizName, err)
	}

	// 通过 defer 封装资源释放逻辑，并增加错误日志
	defer func() {
		if err := rows.Close(); err != nil {
			log.Printf("警告: 关闭视图配置结果集失败 (业务 '%s'): %v", bizName, err)
		}
	}()

	results := make(map[string][]*domain.ViewConfig)

	for rows.Next() {
		var tableName string
		var configJSON string

		// 扫描每一行的表名与配置JSON
		if err := rows.Scan(&tableName, &configJSON); err != nil {
			log.Printf("警告: [AdminConfigService DB] 扫描视图配置行失败 (业务 '%s'): %v", bizName, err)
			continue
		}

		var viewConf domain.ViewConfig
		if err := json.Unmarshal([]byte(configJSON), &viewConf); err != nil {
			log.Printf("警告: [AdminConfigService DB] JSON解析失败 (业务 '%s', 表 '%s')，数据: %s，错误: %v", bizName, tableName, configJSON, err)
			continue
		}

		// 追加到对应表的视图列表中
		results[tableName] = append(results[tableName], &viewConf)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("处理业务 '%s' 的视图配置列表时出错: %w", bizName, err)
	}

	return results, nil
}

// UpdateAllViewsForBiz 在单个事务中，原子性地全量更新一个业务组的所有视图配置。
// 该操作会先删除业务组的所有现有视图配置，然后插入传入的所有新配置。
func (s *AdminConfigServiceImpl) UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) (err error) {
	if bizName == "" {
		return fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	// 开启事务
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败 (业务 '%s'): %w", bizName, err)
	}

	// 管理事务提交 / 回滚逻辑
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			log.Printf("严重错误: UpdateAllViewsForBiz 触发 panic，事务已回滚 (业务 '%s'): %v", bizName, p)
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
			log.Printf("警告: UpdateAllViewsForBiz 执行失败，事务已回滚 (业务 '%s'): %v", bizName, err)
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				err = fmt.Errorf("提交事务失败 (业务 '%s'): %w", bizName, commitErr)
			}
		}
	}()

	// 清空旧配置
	if _, err = tx.ExecContext(ctx, "DELETE FROM biz_view_definitions WHERE biz_name = ?", bizName); err != nil {
		return fmt.Errorf("清除旧视图配置失败 (业务 '%s'): %w", bizName, err)
	}

	if len(viewsData) == 0 {
		// 如果没有传入新的视图数据，则只删除旧配置即可
		return nil
	}

	// 准备插入新配置的语句
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO biz_view_definitions 
        (biz_name, table_name, view_name, view_config_json, is_default) 
        VALUES (?, ?, ?, ?, ?)
    `)
	if err != nil {
		return fmt.Errorf("准备插入视图配置失败 (业务 '%s'): %w", bizName, err)
	}
	defer func() {
		if errClose := stmt.Close(); errClose != nil {
			log.Printf("警告: 关闭 stmt 失败 (业务 '%s'): %v", bizName, errClose)
		}
	}()

	// 插入新配置
	for tableName, views := range viewsData {
		for _, view := range views {
			if view == nil {
				continue
			}
			configJSON, errMarshal := json.Marshal(view)
			if errMarshal != nil {
				return fmt.Errorf("序列化视图配置 '%s' (表 '%s', 业务 '%s') 失败: %w", view.ViewName, tableName, bizName, errMarshal)
			}
			if _, errExec := stmt.ExecContext(ctx, bizName, tableName, view.ViewName, string(configJSON), view.IsDefault); errExec != nil {
				return fmt.Errorf("插入视图配置 '%s' (表 '%s', 业务 '%s') 失败: %w", view.ViewName, tableName, bizName, errExec)
			}
		}
	}

	return nil // 事务提交由 defer 完成
}
