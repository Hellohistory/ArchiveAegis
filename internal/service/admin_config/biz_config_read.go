// Package admin_config internal/service/admin_config/biz_config_read.go
package admin_config

import (
	"context"
	"fmt"
	"log"

	"ArchiveAegis/internal/core/domain"
)

// GetBizQueryConfig 从数据库或缓存中获取指定业务组的查询配置。
func (s *AdminConfigServiceImpl) GetBizQueryConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	if bizName == "" {
		return nil, fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	// 尝试从缓存获取
	config, found := s.cache.Get(bizName)
	if found {
		return config, nil
	}

	// 缓存未命中，从数据库加载
	dbConfig, err := s.loadBizQueryConfigFromDB(ctx, bizName)
	if err != nil {
		return nil, err
	}

	// 加载成功则加入缓存
	if dbConfig != nil {
		s.cache.Add(bizName, dbConfig)
	}
	return dbConfig, nil
}

// GetAllConfiguredBizNames 从 biz_overall_settings 表中检索所有已配置业务组的名称列表。
func (s *AdminConfigServiceImpl) GetAllConfiguredBizNames(ctx context.Context) ([]string, error) {
	query := `SELECT biz_name FROM biz_overall_settings ORDER BY biz_name`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("查询业务组列表失败: %w", err)
	}

	// 安全释放资源并记录错误
	defer func() {
		if errClose := rows.Close(); errClose != nil {
			log.Printf("警告: 查询业务组列表后关闭 rows 失败: %v", errClose)
		}
	}()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("扫描业务组名称失败: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代业务组名称列表失败: %w", err)
	}

	return names, nil
}
