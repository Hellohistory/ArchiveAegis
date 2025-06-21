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

// loadBizQueryConfigFromDB 实际从数据库加载完整业务组配置。
// 优先从缓存读取，缓存miss时加载，完成后自动更新缓存。
func (s *AdminConfigServiceImpl) loadBizQueryConfigFromDB(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	if bizName == "" {
		return nil, errors.New("bizName 不能为空")
	}

	// 查询总体配置
	bizConfig, err := s.queryBizOverallConfig(ctx, bizName)
	if err != nil || bizConfig == nil {
		return bizConfig, err // err为nil且bizConfig为nil时为“未配置”，否则为错误
	}

	// 查询所有业务表及其配置
	tables, err := s.queryBizTables(ctx, bizName)
	if err != nil {
		return nil, err
	}

	bizConfig.Tables = tables

	// 更新缓存（如果有必要）
	s.cache.Add(bizName, bizConfig)

	return bizConfig, nil
}

// queryBizOverallConfig 查询业务组整体配置。
func (s *AdminConfigServiceImpl) queryBizOverallConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	var isPubliclySearchable bool
	var defaultQueryTableNullable sql.NullString

	err := s.db.QueryRowContext(ctx,
		`SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings WHERE biz_name = ?`,
		bizName,
	).Scan(&isPubliclySearchable, &defaultQueryTableNullable)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // 业务未配置，不是错误
	}
	if err != nil {
		return nil, fmt.Errorf("查询业务组 '%s' 总体配置失败: %w", bizName, err)
	}

	cfg := &domain.BizQueryConfig{
		BizName:              bizName,
		IsPubliclySearchable: isPubliclySearchable,
		DefaultQueryTable:    "",
		Tables:               make(map[string]*domain.TableConfig),
	}
	if defaultQueryTableNullable.Valid {
		cfg.DefaultQueryTable = defaultQueryTableNullable.String
	}
	return cfg, nil
}

// queryBizTables 查询业务组下所有业务表的配置和字段信息。
func (s *AdminConfigServiceImpl) queryBizTables(ctx context.Context, bizName string) (map[string]*domain.TableConfig, error) {
	tables := make(map[string]*domain.TableConfig)

	queryTables := `
		SELECT table_name, is_searchable, allow_create, allow_update, allow_delete
		FROM biz_searchable_tables WHERE biz_name = ?
	`
	rows, err := s.db.QueryContext(ctx, queryTables, bizName)
	if err != nil {
		return nil, fmt.Errorf("查询业务组 '%s' 可配置表失败: %w", bizName, err)
	}
	defer rows.Close()

	for rows.Next() {
		tc := &domain.TableConfig{
			Fields: make(map[string]domain.FieldSetting),
		}
		if err := rows.Scan(&tc.TableName, &tc.IsSearchable, &tc.AllowCreate, &tc.AllowUpdate, &tc.AllowDelete); err != nil {
			log.Printf("警告: [AdminConfigService] 扫描业务 '%s' 的表配置失败: %v，已跳过该表", bizName, err)
			continue
		}

		fields, err := s.queryTableFields(ctx, bizName, tc.TableName)
		if err != nil {
			log.Printf("错误: [AdminConfigService] 查询表字段失败(业务 '%s', 表 '%s'): %v", bizName, tc.TableName, err)
			tc.Fields = map[string]domain.FieldSetting{}
		} else {
			tc.Fields = fields
		}

		tables[tc.TableName] = tc
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历业务组 '%s' 可配置表失败: %w", bizName, err)
	}

	return tables, nil
}

// queryTableFields 查询单表所有字段的详细配置。
func (s *AdminConfigServiceImpl) queryTableFields(ctx context.Context, bizName, tableName string) (map[string]domain.FieldSetting, error) {
	fields := make(map[string]domain.FieldSetting)

	rows, err := s.db.QueryContext(ctx,
		`SELECT field_name, is_searchable, is_returnable, data_type
		 FROM biz_table_field_settings
		 WHERE biz_name = ? AND table_name = ?`,
		bizName, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var fs domain.FieldSetting
		if err := rows.Scan(&fs.FieldName, &fs.IsSearchable, &fs.IsReturnable, &fs.DataType); err != nil {
			log.Printf("警告: [AdminConfigService] 扫描字段失败(业务 '%s', 表 '%s'): %v，已跳过", bizName, tableName, err)
			continue
		}
		fields[fs.FieldName] = fs
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历表字段失败(业务 '%s', 表 '%s'): %w", bizName, tableName, err)
	}

	return fields, nil
}
