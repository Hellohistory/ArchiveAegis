// Package admin_config internal/service/admin_config/admin_config_service.go
package admin_config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"

	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

// AdminConfigServiceImpl 是 QueryAdminConfigService 的一个实现。
// 它负责管理业务、表、字段、视图和速率限制等各种系统配置，并提供缓存机制以提高性能。
type AdminConfigServiceImpl struct {
	db    *sql.DB
	cache *lru.LRU[string, *domain.BizQueryConfig]
}

// 静态断言，确保 AdminConfigServiceImpl 实现了 port.QueryAdminConfigService 接口。
var _ port.QueryAdminConfigService = (*AdminConfigServiceImpl)(nil)

// NewAdminConfigServiceImpl 创建一个新的 AdminConfigServiceImpl 实例。
// authDB: 认证数据库连接实例。
// maxCacheEntries: 缓存中允许的最大条目数。
// defaultCacheTTL: 缓存条目的默认过期时间。
func NewAdminConfigServiceImpl(authDB *sql.DB, maxCacheEntries int, defaultCacheTTL time.Duration) (*AdminConfigServiceImpl, error) {
	if authDB == nil {
		return nil, fmt.Errorf("AdminConfigServiceImpl 初始化失败: authDB 实例不能为 nil")
	}
	if maxCacheEntries <= 0 {
		maxCacheEntries = 1000 // 默认值
	}
	if defaultCacheTTL <= 0 {
		defaultCacheTTL = 5 * time.Minute // 默认值
	}

	// 初始化一个带有过期时间的 LRU 缓存
	lruCacheInstance := lru.NewLRU[string, *domain.BizQueryConfig](maxCacheEntries, nil, defaultCacheTTL)

	return &AdminConfigServiceImpl{
		db:    authDB,
		cache: lruCacheInstance,
	}, nil
}

// InvalidateCacheForBiz 手动使指定业务组的缓存失效。
func (s *AdminConfigServiceImpl) InvalidateCacheForBiz(bizName string) {
	if bizName == "" {
		return
	}
	s.cache.Remove(bizName)
	log.Printf("信息: [AdminConfigService] 业务 '%s' 的查询配置LRU缓存已失效。", bizName)
}

// InvalidateAllCaches 清除所有缓存。
func (s *AdminConfigServiceImpl) InvalidateAllCaches() {
	s.cache.Purge()
	log.Printf("信息: [AdminConfigService] 所有查询配置LRU缓存已清除。")
}

// loadBizQueryConfigFromDB 是实际从数据库加载完整业务组配置的内部方法。
// 这是一个辅助方法，因为它在其他 Get 方法中被调用。
func (s *AdminConfigServiceImpl) loadBizQueryConfigFromDB(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	bizConfig := &domain.BizQueryConfig{
		BizName: bizName,
		Tables:  make(map[string]*domain.TableConfig),
	}

	// 查询总体配置
	var defaultQueryTableNullable sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings WHERE biz_name = ?",
		bizName).Scan(&bizConfig.IsPubliclySearchable, &defaultQueryTableNullable)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // 非错误，仅未配置
	}
	if err != nil {
		return nil, fmt.Errorf("获取业务组 '%s' 总体配置失败: %w", bizName, err)
	}
	if defaultQueryTableNullable.Valid {
		bizConfig.DefaultQueryTable = defaultQueryTableNullable.String
	}

	// 查询所有配置表
	queryTables := `
        SELECT table_name, is_searchable, allow_create, allow_update, allow_delete
        FROM biz_searchable_tables WHERE biz_name = ?
    `
	rowsTables, err := s.db.QueryContext(ctx, queryTables, bizName)
	if err != nil {
		return nil, fmt.Errorf("获取业务组 '%s' 可配置表列表失败: %w", bizName, err)
	}
	defer func() {
		if err := rowsTables.Close(); err != nil {
			log.Printf("警告: 关闭业务表结果集失败 (业务 '%s'): %v", bizName, err)
		}
	}()

	// 遍历每张表，加载字段配置
	for rowsTables.Next() {
		currentTableConfig := &domain.TableConfig{
			Fields: make(map[string]domain.FieldSetting),
		}

		if errScan := rowsTables.Scan(
			&currentTableConfig.TableName,
			&currentTableConfig.IsSearchable,
			&currentTableConfig.AllowCreate,
			&currentTableConfig.AllowUpdate,
			&currentTableConfig.AllowDelete,
		); errScan != nil {
			log.Printf("警告: [AdminConfigService] 扫描业务组 '%s' 的表配置失败: %v。已跳过此表。", bizName, errScan)
			continue
		}

		// 查询字段配置（封装成闭包以处理 Close）
		func(tableName string, tc *domain.TableConfig) {
			fieldRows, errFieldQuery := s.db.QueryContext(ctx,
				`SELECT field_name, is_searchable, is_returnable, data_type
                 FROM biz_table_field_settings
                 WHERE biz_name = ? AND table_name = ?`,
				bizName, tableName)
			if errFieldQuery != nil {
				log.Printf("错误: [AdminConfigService DB] 查询字段失败 (业务 '%s', 表 '%s'): %v。跳过此表。", bizName, tableName, errFieldQuery)
				return
			}
			defer func() {
				if err := fieldRows.Close(); err != nil {
					log.Printf("警告: 关闭字段结果集失败 (业务 '%s', 表 '%s'): %v", bizName, tableName, err)
				}
			}()

			for fieldRows.Next() {
				var fs domain.FieldSetting
				if errScan := fieldRows.Scan(&fs.FieldName, &fs.IsSearchable, &fs.IsReturnable, &fs.DataType); errScan != nil {
					log.Printf("错误: [AdminConfigService DB] 扫描字段失败 (业务 '%s', 表 '%s'): %v。跳过此字段。", bizName, tableName, errScan)
					continue
				}
				tc.Fields[fs.FieldName] = fs
			}
			if errIter := fieldRows.Err(); errIter != nil {
				log.Printf("错误: [AdminConfigService DB] 迭代字段结果失败 (业务 '%s', 表 '%s'): %v。", bizName, tableName, errIter)
			}
		}(currentTableConfig.TableName, currentTableConfig)

		// 加入配置集
		bizConfig.Tables[currentTableConfig.TableName] = currentTableConfig
	}

	if errRows := rowsTables.Err(); errRows != nil {
		return nil, fmt.Errorf("处理业务组 '%s' 可配置表列表时出错: %w", bizName, errRows)
	}

	return bizConfig, nil
}
