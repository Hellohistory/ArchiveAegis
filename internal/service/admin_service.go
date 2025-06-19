// Package service file: internal/service/admin_service.go
package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"

	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

// AdminConfigServiceImpl 是 QueryAdminConfigService 的一个实现
type AdminConfigServiceImpl struct {
	db    *sql.DB
	cache *lru.LRU[string, *domain.BizQueryConfig]
}

// 静态断言，确保 AdminConfigServiceImpl 实现了接口
var _ port.QueryAdminConfigService = (*AdminConfigServiceImpl)(nil)

// NewAdminConfigServiceImpl 创建一个新的 AdminConfigServiceImpl 实例。
func NewAdminConfigServiceImpl(authDB *sql.DB, maxCacheEntries int, defaultCacheTTL time.Duration) (*AdminConfigServiceImpl, error) {
	if authDB == nil {
		return nil, fmt.Errorf("AdminConfigServiceImpl 初始化失败: authDB 实例不能为 nil")
	}
	if maxCacheEntries <= 0 {
		maxCacheEntries = 1000
	}
	if defaultCacheTTL <= 0 {
		defaultCacheTTL = 5 * time.Minute
	}

	lruCacheInstance := lru.NewLRU[string, *domain.BizQueryConfig](maxCacheEntries, nil, defaultCacheTTL)

	return &AdminConfigServiceImpl{
		db:    authDB,
		cache: lruCacheInstance,
	}, nil
}

// GetBizQueryConfig 从数据库或缓存中获取指定业务组的查询配置。
func (s *AdminConfigServiceImpl) GetBizQueryConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	if bizName == "" {
		return nil, fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	config, found := s.cache.Get(bizName)
	if found {
		return config, nil
	}

	dbConfig, err := s.loadBizQueryConfigFromDB(ctx, bizName)
	if err != nil {
		return nil, err
	}

	if dbConfig != nil {
		s.cache.Add(bizName, dbConfig)
	}
	return dbConfig, nil
}

// loadBizQueryConfigFromDB 是实际从数据库加载配置的内部方法。
func (s *AdminConfigServiceImpl) loadBizQueryConfigFromDB(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	bizConfig := &domain.BizQueryConfig{
		BizName: bizName,
		Tables:  make(map[string]*domain.TableConfig),
	}

	var defaultQueryTableNullable sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings WHERE biz_name = ?",
		bizName).Scan(&bizConfig.IsPubliclySearchable, &defaultQueryTableNullable)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // Not an error, just no config found
	}
	if err != nil {
		return nil, fmt.Errorf("获取业务组 '%s' 总体配置失败: %w", bizName, err)
	}
	if defaultQueryTableNullable.Valid {
		bizConfig.DefaultQueryTable = defaultQueryTableNullable.String
	}

	queryTables := `
        SELECT table_name, is_searchable, allow_create, allow_update, allow_delete
        FROM biz_searchable_tables WHERE biz_name = ?
    `
	rowsTables, err := s.db.QueryContext(ctx, queryTables, bizName)
	if err != nil {
		return nil, fmt.Errorf("获取业务组 '%s' 可配置表列表失败: %w", bizName, err)
	}
	defer rowsTables.Close()

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

		// 5. 加载该表的字段配置 (这部分逻辑不变)
		fieldRows, errFieldQuery := s.db.QueryContext(ctx,
			`SELECT field_name, is_searchable, is_returnable, data_type
           FROM biz_table_field_settings
           WHERE biz_name = ? AND table_name = ?`,
			bizName, currentTableConfig.TableName)

		if errFieldQuery != nil {
			log.Printf("错误: [AdminConfigService DB] 查询字段失败 (业务 '%s', 表 '%s'): %v. 跳过此表。", bizName, currentTableConfig.TableName, errFieldQuery)
			continue
		}

		// 使用 defer in a loop 的标准做法
		func() {
			defer fieldRows.Close()
			for fieldRows.Next() {
				var fs domain.FieldSetting
				if errScan := fieldRows.Scan(&fs.FieldName, &fs.IsSearchable, &fs.IsReturnable, &fs.DataType); errScan != nil {
					log.Printf("错误: [AdminConfigService DB] 扫描字段失败 (业务 '%s', 表 '%s'): %v. 跳过此字段。", bizName, currentTableConfig.TableName, errScan)
					continue
				}
				currentTableConfig.Fields[fs.FieldName] = fs
			}
		}()
		if errFieldRows := fieldRows.Err(); errFieldRows != nil {
			log.Printf("错误: [AdminConfigService DB] 迭代字段结果失败 (业务 '%s', 表 '%s'): %v.", bizName, currentTableConfig.TableName, errFieldRows)
		}

		bizConfig.Tables[currentTableConfig.TableName] = currentTableConfig
	}
	if errRows := rowsTables.Err(); errRows != nil {
		return nil, fmt.Errorf("处理业务组 '%s' 可配置表列表时出错: %w", bizName, errRows)
	}

	return bizConfig, nil
}

// GetDefaultViewConfig 从数据库获取指定表的默认视图配置
func (s *AdminConfigServiceImpl) GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error) {
	if bizName == "" || tableName == "" {
		return nil, fmt.Errorf("业务组和表名不能为空")
	}

	var configJSON string
	query := `SELECT view_config_json FROM biz_view_definitions WHERE biz_name = ? AND table_name = ? AND is_default = TRUE LIMIT 1`

	err := s.db.QueryRowContext(ctx, query, bizName, tableName).Scan(&configJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // Not an error
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

// GetAllViewConfigsForBiz 从数据库获取指定业务组下所有表的全部视图配置
func (s *AdminConfigServiceImpl) GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error) {
	if bizName == "" {
		return nil, fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	query := `SELECT table_name, view_config_json FROM biz_view_definitions WHERE biz_name = ?`
	rows, err := s.db.QueryContext(ctx, query, bizName)
	if err != nil {
		return nil, fmt.Errorf("获取业务 '%s' 的所有视图配置时发生数据库错误: %w", bizName, err)
	}
	defer rows.Close()

	results := make(map[string][]*domain.ViewConfig)
	for rows.Next() {
		var tableName, configJSON string
		if err := rows.Scan(&tableName, &configJSON); err != nil {
			log.Printf("警告: [AdminConfigService DB] 扫描视图配置行失败 (业务 '%s'): %v", bizName, err)
			continue
		}

		var viewConf domain.ViewConfig
		if err := json.Unmarshal([]byte(configJSON), &viewConf); err != nil {
			log.Printf("警告: [AdminConfigService DB] 解析视图配置JSON失败 (业务 '%s', 表 '%s'): %v", bizName, tableName, err)
			continue
		}
		results[tableName] = append(results[tableName], &viewConf)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("处理业务 '%s' 的视图配置列表时出错: %w", bizName, err)
	}
	return results, nil
}

// UpdateAllViewsForBiz 在单个事务中，原子性地全量更新一个业务组的所有视图配置
func (s *AdminConfigServiceImpl) UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) (err error) {
	if bizName == "" {
		return fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("数据库操作失败: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	if _, err = tx.ExecContext(ctx, "DELETE FROM biz_view_definitions WHERE biz_name = ?", bizName); err != nil {
		return fmt.Errorf("更新视图配置时，清除旧数据失败: %w", err)
	}
	if len(viewsData) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO biz_view_definitions 
        (biz_name, table_name, view_name, view_config_json, is_default) 
        VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("数据库操作失败: %w", err)
	}
	defer stmt.Close()

	for tableName, views := range viewsData {
		for _, view := range views {
			if view == nil {
				continue
			}
			configJSON, errMarshal := json.Marshal(view)
			if errMarshal != nil {
				return fmt.Errorf("序列化视图配置 '%s' (表 '%s') 失败: %w", view.ViewName, tableName, errMarshal)
			}
			if _, errExec := stmt.ExecContext(ctx, bizName, tableName, view.ViewName, string(configJSON), view.IsDefault); errExec != nil {
				return fmt.Errorf("插入视图配置 '%s' (表 '%s') 失败: %w", view.ViewName, tableName, errExec)
			}
		}
	}
	return nil
}

// GetAllConfiguredBizNames 从 biz_overall_settings 表中检索所有已配置业务组的名称列表。
func (s *AdminConfigServiceImpl) GetAllConfiguredBizNames(ctx context.Context) ([]string, error) {
	query := `SELECT biz_name FROM biz_overall_settings ORDER BY biz_name;`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("数据库查询失败: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("数据库扫描失败: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代数据库结果时出错: %w", err)
	}
	return names, nil
}

// GetIPLimitSettings 获取全局IP速率限制配置
func (s *AdminConfigServiceImpl) GetIPLimitSettings(ctx context.Context) (*domain.IPLimitSetting, error) {
	settings := &domain.IPLimitSetting{}
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM global_settings WHERE key IN (?, ?)", "ip_rate_limit_per_minute", "ip_burst_size")
	if err != nil {
		return nil, fmt.Errorf("数据库查询失败: %w", err)
	}
	defer rows.Close()

	foundKeys := 0
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("数据库扫描失败: %w", err)
		}
		switch key {
		case "ip_rate_limit_per_minute":
			if v, errConv := strconv.ParseFloat(value, 64); errConv == nil {
				settings.RateLimitPerMinute = v
				foundKeys++
			}
		case "ip_burst_size":
			if v, errConv := strconv.Atoi(value); errConv == nil {
				settings.BurstSize = v
				foundKeys++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代数据库结果时出错: %w", err)
	}
	if foundKeys < 2 {
		return nil, nil // Not an error, just means no settings found
	}
	return settings, nil
}

// UpdateIPLimitSettings 更新全局IP速率限制配置
func (s *AdminConfigServiceImpl) UpdateIPLimitSettings(ctx context.Context, settings domain.IPLimitSetting) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO global_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return fmt.Errorf("准备SQL语句失败: %w", err)
	}
	defer stmt.Close()

	if _, err = stmt.ExecContext(ctx, "ip_rate_limit_per_minute", fmt.Sprintf("%f", settings.RateLimitPerMinute)); err != nil {
		return fmt.Errorf("更新 ip_rate_limit_per_minute 失败: %w", err)
	}
	if _, err = stmt.ExecContext(ctx, "ip_burst_size", strconv.Itoa(settings.BurstSize)); err != nil {
		return fmt.Errorf("更新 ip_burst_size 失败: %w", err)
	}
	return nil
}

// GetUserLimitSettings 获取特定用户的速率限制配置
func (s *AdminConfigServiceImpl) GetUserLimitSettings(ctx context.Context, userID int64) (*domain.UserLimitSetting, error) {
	var rateLimit sql.NullFloat64
	var burstSize sql.NullInt64
	query := "SELECT rate_limit_per_second, burst_size FROM _user WHERE id = ?"
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&rateLimit, &burstSize)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("数据库查询失败: %w", err)
	}
	if !rateLimit.Valid || !burstSize.Valid {
		return nil, nil
	}
	return &domain.UserLimitSetting{
		RateLimitPerSecond: rateLimit.Float64,
		BurstSize:          int(burstSize.Int64),
	}, nil
}

// UpdateUserLimitSettings 更新特定用户的速率限制配置
func (s *AdminConfigServiceImpl) UpdateUserLimitSettings(ctx context.Context, userID int64, settings domain.UserLimitSetting) error {
	query := "UPDATE _user SET rate_limit_per_second = ?, burst_size = ? WHERE id = ?"
	result, err := s.db.ExecContext(ctx, query, settings.RateLimitPerSecond, settings.BurstSize, userID)
	if err != nil {
		return fmt.Errorf("数据库更新失败: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("用户ID %d 不存在", userID)
	}
	return nil
}

// GetBizRateLimitSettings 获取特定业务组的速率限制配置
func (s *AdminConfigServiceImpl) GetBizRateLimitSettings(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error) {
	query := "SELECT rate_limit_per_second, burst_size FROM biz_ratelimit_settings WHERE biz_name = ?"
	setting := &domain.BizRateLimitSetting{}
	err := s.db.QueryRowContext(ctx, query, bizName).Scan(&setting.RateLimitPerSecond, &setting.BurstSize)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("数据库查询失败: %w", err)
	}
	return setting, nil
}

// UpdateBizRateLimitSettings 更新特定业务组的速率限制配置
func (s *AdminConfigServiceImpl) UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings domain.BizRateLimitSetting) error {
	query := `
        INSERT INTO biz_ratelimit_settings (biz_name, rate_limit_per_second, burst_size) 
        VALUES (?, ?, ?) 
        ON CONFLICT(biz_name) DO UPDATE SET 
            rate_limit_per_second = excluded.rate_limit_per_second, 
            burst_size = excluded.burst_size`
	_, err := s.db.ExecContext(ctx, query, bizName, settings.RateLimitPerSecond, settings.BurstSize)
	if err != nil {
		return fmt.Errorf("数据库更新失败: %w", err)
	}
	return nil
}

// InvalidateCacheForBiz 手动使指定业务组的缓存失效
func (s *AdminConfigServiceImpl) InvalidateCacheForBiz(bizName string) {
	if bizName == "" {
		return
	}
	s.cache.Remove(bizName)
	log.Printf("信息: [AdminConfigService] 业务 '%s' 的查询配置LRU缓存已失效。", bizName)
}

// InvalidateAllCaches 清除所有缓存
func (s *AdminConfigServiceImpl) InvalidateAllCaches() {
	s.cache.Purge()
	log.Printf("信息: [AdminConfigService] 所有查询配置LRU缓存已清除。")
}

// UpdateTableWritePermissions 更新指定表的写权限设置
func (s *AdminConfigServiceImpl) UpdateTableWritePermissions(ctx context.Context, bizName, tableName string, perms domain.TableConfig) error {
	// 为了确保数据一致性，我们使用一个更健壮的 "INSERT ... ON CONFLICT DO UPDATE" 语句。
	// 这可以处理表在 biz_searchable_tables 中尚不存在，但用户想直接为其设置权限的情况。
	// 首先，需要确保 biz_name 存在于 biz_overall_settings 中。
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM biz_overall_settings WHERE biz_name = ?", bizName).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("业务组 '%s' 不存在，无法为其下的表设置权限", bizName)
		}
		return fmt.Errorf("检查业务组是否存在时出错: %w", err)
	}

	query := `
        INSERT INTO biz_searchable_tables (biz_name, table_name, allow_create, allow_update, allow_delete)
        VALUES (?, ?, ?, ?, ?)
        ON CONFLICT(biz_name, table_name) DO UPDATE SET
            allow_create = excluded.allow_create,
            allow_update = excluded.allow_update,
            allow_delete = excluded.allow_delete;
    `
	_, err = s.db.ExecContext(ctx, query, bizName, tableName, perms.AllowCreate, perms.AllowUpdate, perms.AllowDelete)
	if err != nil {
		return fmt.Errorf("更新表 '%s' 的写权限失败: %w", tableName, err)
	}

	// 关键一步：在配置变更后，立即让相关的缓存失效！
	s.InvalidateCacheForBiz(bizName)
	log.Printf("信息: [AdminConfigService] 表 '%s/%s' 的写权限已更新，相关缓存已失效。", bizName, tableName)

	return nil
}
