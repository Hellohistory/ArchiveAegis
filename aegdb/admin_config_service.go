// Package aegdb admin_config_service.go
package aegdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

/*
================================================================================
  QueryAdminConfigService 接口及相关数据传输对象 (DTOs) 定义
================================================================================
*/

// CardBinding 定义了卡片视图的字段如何与数据源绑定
type CardBinding struct {
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Description string `json:"description"`
	ImageUrl    string `json:"imageUrl"`
	Tag         string `json:"tag"`
}

// TableColumnBinding 定义了表格视图中单列的配置
type TableColumnBinding struct {
	Field       string `json:"field"`
	DisplayName string `json:"displayName"`
	Format      string `json:"format,omitempty"` // 可选的格式化指令, e.g., "date"
}

// TableBinding 定义了表格视图的配置
type TableBinding struct {
	Columns []TableColumnBinding `json:"columns"`
}

// ViewBinding 包含了所有可能的视图类型的绑定配置
type ViewBinding struct {
	Card  *CardBinding  `json:"card,omitempty"`
	Table *TableBinding `json:"table,omitempty"`
}

// ViewConfig 是一个完整的视图配置对象，代表一种展示方案
type ViewConfig struct {
	ViewName    string      `json:"view_name"`
	ViewType    string      `json:"view_type"`
	DisplayName string      `json:"display_name"`
	IsDefault   bool        `json:"is_default"`
	Binding     ViewBinding `json:"binding"`
}

// FieldSetting 定义了单个字段的查询和返回配置
type FieldSetting struct {
	FieldName    string `json:"field_name"`
	IsSearchable bool   `json:"is_searchable"`
	IsReturnable bool   `json:"is_returnable"`
	DataType     string `json:"dataType"`
}

// TableConfig 定义了单个表的查询配置
type TableConfig struct {
	TableName    string                  `json:"table_name"`
	IsSearchable bool                    `json:"is_searchable"`
	Fields       map[string]FieldSetting `json:"fields"`
}

// BizQueryConfig 定义了单个业务组的完整查询配置
type BizQueryConfig struct {
	BizName              string                  `json:"biz_name"`
	IsPubliclySearchable bool                    `json:"is_publicly_searchable"`
	DefaultQueryTable    string                  `json:"default_query_table"`
	Tables               map[string]*TableConfig `json:"tables"`
}

// IPLimitSetting 定义了全局IP速率限制的配置
type IPLimitSetting struct {
	RateLimitPerMinute float64 `json:"rate_limit_per_minute"`
	BurstSize          int     `json:"burst_size"`
}

// UserLimitSetting 定义了单个用户的速率限制配置
type UserLimitSetting struct {
	RateLimitPerSecond float64 `json:"rate_limit_per_second"`
	BurstSize          int     `json:"burst_size"`
}

// BizRateLimitSetting 定义了单个业务组的速率限制配置
type BizRateLimitSetting struct {
	RateLimitPerSecond float64 `json:"rate_limit_per_second"`
	BurstSize          int     `json:"burst_size"`
}

// QueryAdminConfigService 是一个接口，Manager 将通过它获取管理员定义的查询配置。
type QueryAdminConfigService interface {
	GetBizQueryConfig(ctx context.Context, bizName string) (*BizQueryConfig, error)
	GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*ViewConfig, error)
	GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*ViewConfig, error)
	UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*ViewConfig) error
	InvalidateCacheForBiz(bizName string)
	InvalidateAllCaches()

	// GetIPLimitSettings 速率限制相关的接口

	GetIPLimitSettings(ctx context.Context) (*IPLimitSetting, error)
	UpdateIPLimitSettings(ctx context.Context, settings IPLimitSetting) error
	GetUserLimitSettings(ctx context.Context, userID int64) (*UserLimitSetting, error)
	UpdateUserLimitSettings(ctx context.Context, userID int64, settings UserLimitSetting) error
	GetBizRateLimitSettings(ctx context.Context, bizName string) (*BizRateLimitSetting, error)
	UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings BizRateLimitSetting) error
}

/*
================================================================================
  AdminConfigServiceImpl: QueryAdminConfigService 的具体实现
================================================================================
*/

// AdminConfigServiceImpl 是 QueryAdminConfigService 的一个实现
type AdminConfigServiceImpl struct {
	db    *sql.DB
	cache *lru.LRU[string, *BizQueryConfig]
}

// NewAdminConfigServiceImpl 创建一个新的 AdminConfigServiceImpl 实例。
func NewAdminConfigServiceImpl(authDB *sql.DB, maxCacheEntries int, defaultCacheTTL time.Duration) (*AdminConfigServiceImpl, error) {
	if authDB == nil {
		return nil, fmt.Errorf("AdminConfigServiceImpl 初始化失败: authDB 实例不能为 nil")
	}
	if maxCacheEntries <= 0 {
		maxCacheEntries = 1000
		log.Printf("[AdminConfigService] maxCacheEntries 未设置或无效，使用默认值: %d", maxCacheEntries)
	}
	if defaultCacheTTL <= 0 {
		defaultCacheTTL = 5 * time.Minute
		log.Printf("[AdminConfigService] defaultCacheTTL 未设置或无效，使用默认值: %v", defaultCacheTTL)
	}

	lruCacheInstance := lru.NewLRU[string, *BizQueryConfig](maxCacheEntries, nil, defaultCacheTTL)

	initStmts := []string{
		`CREATE TABLE IF NOT EXISTS biz_overall_settings (
          biz_name TEXT PRIMARY KEY,
          is_publicly_searchable BOOLEAN DEFAULT TRUE NOT NULL,
          default_query_table TEXT
       );`,
		`CREATE TABLE IF NOT EXISTS biz_searchable_tables (
          biz_name TEXT NOT NULL,
          table_name TEXT NOT NULL,
          PRIMARY KEY (biz_name, table_name),
          FOREIGN KEY (biz_name) REFERENCES biz_overall_settings(biz_name) ON DELETE CASCADE ON UPDATE CASCADE
       );`,
		`CREATE TABLE IF NOT EXISTS biz_table_field_settings (
            biz_name TEXT NOT NULL,
            table_name TEXT NOT NULL,
            field_name TEXT NOT NULL,
            is_searchable BOOLEAN DEFAULT FALSE NOT NULL,
            is_returnable BOOLEAN DEFAULT FALSE NOT NULL,
            return_alias TEXT,
            default_return_order INTEGER DEFAULT 0 NOT NULL,
            data_type TEXT DEFAULT 'string' NOT NULL,
            PRIMARY KEY (biz_name, table_name, field_name),
            FOREIGN KEY (biz_name, table_name) REFERENCES biz_searchable_tables(biz_name, table_name) ON DELETE CASCADE ON UPDATE CASCADE
        );`,
		`CREATE TABLE IF NOT EXISTS biz_view_definitions (
          biz_name TEXT NOT NULL,
          table_name TEXT NOT NULL,
          view_name TEXT NOT NULL,
          view_config_json TEXT NOT NULL,
          is_default BOOLEAN DEFAULT FALSE NOT NULL,
          PRIMARY KEY (biz_name, table_name, view_name)
       );`,
		`CREATE TABLE IF NOT EXISTS global_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			description TEXT,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS biz_ratelimit_settings (
			biz_name TEXT PRIMARY KEY,
			rate_limit_per_second REAL NOT NULL DEFAULT 5.0,
			burst_size INTEGER NOT NULL DEFAULT 10,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
		`INSERT OR IGNORE INTO global_settings (key, value, description) VALUES
			('ip_rate_limit_per_minute', '60', '未认证IP的默认每分钟请求数'),
			('ip_burst_size', '20', '未认证IP的默认瞬时请求峰值');`,

		`CREATE INDEX IF NOT EXISTS idx_bvd_biz_table_default ON biz_view_definitions(biz_name, table_name, is_default);`,
		`CREATE INDEX IF NOT EXISTS idx_bst_biz_name ON biz_searchable_tables(biz_name);`,
		`CREATE INDEX IF NOT EXISTS idx_btfs_biz_table ON biz_table_field_settings(biz_name, table_name);`,
	}
	for _, stmt := range initStmts {
		if _, err := authDB.Exec(stmt); err != nil {
			log.Printf("警告: [AdminConfigService] 执行 auth.db 初始化语句失败 (可能是表已存在或其他原因): %v. SQL: %s", err, stmt)
		}
	}
	log.Printf("[AdminConfigService] 初始化完成，LRU缓存最大条目数: %d, 默认TTL: %v", maxCacheEntries, defaultCacheTTL)

	return &AdminConfigServiceImpl{
		db:    authDB,
		cache: lruCacheInstance,
	}, nil
}

// GetBizQueryConfig 从数据库或缓存中获取指定业务组的查询配置。
func (s *AdminConfigServiceImpl) GetBizQueryConfig(ctx context.Context, bizName string) (*BizQueryConfig, error) {
	if bizName == "" {
		return nil, fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	config, found := s.cache.Get(bizName)
	if found {
		log.Printf("调试: [AdminConfigService] 业务 '%s' 的配置从LRU缓存加载。", bizName)
		return config, nil
	}

	log.Printf("调试: [AdminConfigService] 业务 '%s' 的配置LRU缓存未命中，将从数据库加载。", bizName)
	dbConfig, err := s.loadBizQueryConfigFromDB(ctx, bizName)
	if err != nil {
		return nil, err
	}

	if dbConfig != nil {
		s.cache.Add(bizName, dbConfig)
		log.Printf("调试: [AdminConfigService] 业务 '%s' 的配置已从数据库加载并存入LRU缓存。", bizName)
	} else {
		log.Printf("调试: [AdminConfigService] 业务 '%s' 在数据库中未找到配置，不存入缓存。", bizName)
	}
	return dbConfig, nil
}

// loadBizQueryConfigFromDB 是实际从数据库加载配置的内部方法。
func (s *AdminConfigServiceImpl) loadBizQueryConfigFromDB(ctx context.Context, bizName string) (*BizQueryConfig, error) {
	bizConfig := &BizQueryConfig{
		BizName: bizName,
		Tables:  make(map[string]*TableConfig),
	}

	var defaultQueryTableNullable sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings WHERE biz_name = ?",
		bizName).Scan(&bizConfig.IsPubliclySearchable, &defaultQueryTableNullable)

	if errors.Is(sql.ErrNoRows, err) {
		log.Printf("信息: [AdminConfigService DB] 业务组 '%s' 在 'biz_overall_settings' 表中未找到配置记录。", bizName)
		return nil, nil
	}
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] 查询 'biz_overall_settings' 失败 (业务 '%s'): %v", bizName, err)
		return nil, fmt.Errorf("获取业务组 '%s' 总体配置失败: %w", bizName, err)
	}
	if defaultQueryTableNullable.Valid {
		bizConfig.DefaultQueryTable = defaultQueryTableNullable.String
	}

	rowsTables, err := s.db.QueryContext(ctx,
		"SELECT table_name FROM biz_searchable_tables WHERE biz_name = ?",
		bizName)
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] 查询 'biz_searchable_tables' 失败 (业务 '%s'): %v", bizName, err)
		return nil, fmt.Errorf("获取业务组 '%s' 可搜索表列表失败: %w", bizName, err)
	}

	var searchableTableNamesFound []string
	for rowsTables.Next() {
		var tableName string
		if errScan := rowsTables.Scan(&tableName); errScan != nil {
			rowsTables.Close()
			log.Printf("错误: [AdminConfigService DB] 扫描 'biz_searchable_tables' 的表名失败 (业务 '%s'): %v", bizName, errScan)
			return nil, fmt.Errorf("扫描业务组 '%s' 可搜索表名失败: %w", bizName, errScan)
		}
		searchableTableNamesFound = append(searchableTableNamesFound, tableName)
	}
	if errRows := rowsTables.Err(); errRows != nil {
		rowsTables.Close()
		log.Printf("错误: [AdminConfigService DB] 迭代 'biz_searchable_tables' 结果失败 (业务 '%s'): %v", bizName, errRows)
		return nil, fmt.Errorf("处理业务组 '%s' 可搜索表列表时出错: %w", bizName, errRows)
	}
	rowsTables.Close()

	if len(searchableTableNamesFound) == 0 {
		log.Printf("信息: [AdminConfigService DB] 业务组 '%s' 未配置任何可搜索的表。", bizName)
		return bizConfig, nil
	}

	for _, tableNameFromAdmin := range searchableTableNamesFound {
		currentTableConfig := &TableConfig{
			TableName:    tableNameFromAdmin,
			IsSearchable: true,
			Fields:       make(map[string]FieldSetting),
		}

		fieldRows, errFieldQuery := s.db.QueryContext(ctx,
			`SELECT field_name, is_searchable, is_returnable, data_type
           FROM biz_table_field_settings
           WHERE biz_name = ? AND table_name = ?`,
			bizName, tableNameFromAdmin)

		if errFieldQuery != nil {
			log.Printf("错误: [AdminConfigService DB] 查询字段失败 (业务 '%s', 表 '%s'): %v. 跳过此表。", bizName, tableNameFromAdmin, errFieldQuery)
			continue
		}

		fieldCountForThisTable := 0
		for fieldRows.Next() {
			var fs FieldSetting

			errScan := fieldRows.Scan(
				&fs.FieldName,
				&fs.IsSearchable,
				&fs.IsReturnable,
				&fs.DataType,
			)
			if errScan != nil {
				log.Printf("错误: [AdminConfigService DB] 扫描字段失败 (业务 '%s', 表 '%s'): %v. 跳过此字段。", bizName, tableNameFromAdmin, errScan)
				continue
			}

			currentTableConfig.Fields[fs.FieldName] = fs
			fieldCountForThisTable++
		}
		if errFieldRows := fieldRows.Err(); errFieldRows != nil {
			log.Printf("错误: [AdminConfigService DB] 迭代字段结果失败 (业务 '%s', 表 '%s'): %v.", bizName, tableNameFromAdmin, errFieldRows)
		}
		fieldRows.Close()

		if fieldCountForThisTable > 0 {
			bizConfig.Tables[tableNameFromAdmin] = currentTableConfig
		}
	}

	return bizConfig, nil
}

// GetDefaultViewConfig 从数据库获取指定表的默认视图配置
func (s *AdminConfigServiceImpl) GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*ViewConfig, error) {
	if bizName == "" || tableName == "" {
		return nil, fmt.Errorf("业务组和表名不能为空")
	}

	log.Printf("调试: [AdminConfigService] 将从数据库加载业务 '%s' 表 '%s' 的默认视图配置。", bizName, tableName)

	var configJSON string
	query := `SELECT view_config_json FROM biz_view_definitions WHERE biz_name = ? AND table_name = ? AND is_default = TRUE LIMIT 1`

	err := s.db.QueryRowContext(ctx, query, bizName, tableName).Scan(&configJSON)

	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("信息: [AdminConfigService DB] 未找到业务 '%s' 表 '%s' 的默认视图配置。", bizName, tableName)
		return nil, nil
	}
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] 查询默认视图配置失败 (业务 '%s', 表 '%s'): %v", bizName, tableName, err)
		return nil, fmt.Errorf("获取视图配置时发生数据库错误")
	}

	var viewConf ViewConfig
	if err := json.Unmarshal([]byte(configJSON), &viewConf); err != nil {
		log.Printf("错误: [AdminConfigService DB] 解析视图配置JSON失败 (业务 '%s', 表 '%s'): %v", bizName, tableName, err)
		return nil, fmt.Errorf("视图配置数据格式无效")
	}

	log.Printf("调试: [AdminConfigService] 成功加载并解析了业务 '%s' 表 '%s' 的默认视图 (类型: %s)。", bizName, tableName, viewConf.ViewType)
	return &viewConf, nil
}

// GetAllViewConfigsForBiz 从数据库获取指定业务组下所有表的全部视图配置
func (s *AdminConfigServiceImpl) GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*ViewConfig, error) {
	if bizName == "" {
		return nil, fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	query := `SELECT table_name, view_config_json FROM biz_view_definitions WHERE biz_name = ?`
	rows, err := s.db.QueryContext(ctx, query, bizName)
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] 查询所有视图配置失败 (业务 '%s'): %v", bizName, err)
		return nil, fmt.Errorf("获取业务 '%s' 的所有视图配置时发生数据库错误", bizName)
	}
	defer rows.Close()

	// 使用表名作为key，存储该表下所有的视图配置
	results := make(map[string][]*ViewConfig)

	for rows.Next() {
		var tableName string
		var configJSON string
		if err := rows.Scan(&tableName, &configJSON); err != nil {
			log.Printf("警告: [AdminConfigService DB] 扫描视图配置行失败 (业务 '%s'): %v", bizName, err)
			continue
		}

		var viewConf ViewConfig
		if err := json.Unmarshal([]byte(configJSON), &viewConf); err != nil {
			log.Printf("警告: [AdminConfigService DB] 解析视图配置JSON失败 (业务 '%s', 表 '%s'): %v", bizName, tableName, err)
			continue // 跳过此条记录
		}

		// 将解析出的配置添加到对应表的切片中
		results[tableName] = append(results[tableName], &viewConf)
	}

	if err := rows.Err(); err != nil {
		log.Printf("错误: [AdminConfigService DB] 迭代视图配置结果时发生错误 (业务 '%s'): %v", bizName, err)
		return nil, fmt.Errorf("处理业务 '%s' 的视图配置列表时出错", bizName)
	}

	log.Printf("调试: [AdminConfigService] 成功加载业务 '%s' 的所有视图配置。", bizName)
	return results, nil
}

// UpdateAllViewsForBiz 在单个事务中，原子性地全量更新一个业务组的所有视图配置
func (s *AdminConfigServiceImpl) UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*ViewConfig) (err error) {
	if bizName == "" {
		return fmt.Errorf("业务组名称 (bizName) 不能为空")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] UpdateAllViewsForBiz 开始事务失败: %v", err)
		return fmt.Errorf("数据库操作失败")
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			log.Printf("信息: [AdminConfigService DB] UpdateAllViewsForBiz 因错误回滚事务: %v", err)
			_ = tx.Rollback()
		} else {
			log.Printf("信息: [AdminConfigService DB] UpdateAllViewsForBiz 准备提交事务...")
			err = tx.Commit()
			if err != nil {
				log.Printf("错误: [AdminConfigService DB] UpdateAllViewsForBiz 提交事务失败: %v", err)
			}
		}
	}()

	// 1. 删除该业务组所有旧的视图配置
	if _, err = tx.ExecContext(ctx, "DELETE FROM biz_view_definitions WHERE biz_name = ?", bizName); err != nil {
		log.Printf("错误: [AdminConfigService DB] UpdateAllViewsForBiz 清除旧视图配置失败: %v", err)
		return fmt.Errorf("更新视图配置时，清除旧数据失败")
	}

	// 如果传入的数据为空，则仅执行删除操作
	if len(viewsData) == 0 {
		return nil
	}

	// 2. 准备插入新配置的语句
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO biz_view_definitions 
        (biz_name, table_name, view_name, view_config_json, is_default) 
        VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] UpdateAllViewsForBiz 准备插入语句失败: %v", err)
		return fmt.Errorf("数据库操作失败")
	}
	defer stmt.Close()

	// 3. 遍历传入的数据并插入
	for tableName, views := range viewsData {
		if views == nil {
			continue
		}
		for _, view := range views {
			if view == nil {
				continue
			}

			// 将视图配置对象重新序列化为 JSON 字符串,这里的 view 包含了 IsDefault，所以存入JSON的数据是完整的
			configJSON, errMarshal := json.Marshal(view)
			if errMarshal != nil {
				err = fmt.Errorf("序列化视图配置 '%s' (表 '%s') 失败: %w", view.ViewName, tableName, errMarshal)
				return err // 直接返回，触发 defer 中的回滚
			}

			// 执行插入，view.IsDefault 直接作为参数传入
			if _, errExec := stmt.ExecContext(ctx, bizName, tableName, view.ViewName, string(configJSON), view.IsDefault); errExec != nil {
				err = fmt.Errorf("插入视图配置 '%s' (表 '%s') 失败: %w", view.ViewName, tableName, errExec)
				return err // 直接返回，触发 defer 中的回滚
			}
		}
	}

	// 如果所有操作都成功，外部的 err 变量为 nil，defer 将会提交事务
	return nil
}

// ============== 新增：速率限制配置相关方法 ==============

// GetIPLimitSettings 获取全局IP速率限制配置
func (s *AdminConfigServiceImpl) GetIPLimitSettings(ctx context.Context) (*IPLimitSetting, error) {
	settings := &IPLimitSetting{}
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM global_settings WHERE key IN (?, ?)", "ip_rate_limit_per_minute", "ip_burst_size")
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] 查询IP速率限制配置失败: %v", err)
		return nil, fmt.Errorf("数据库查询失败")
	}
	defer rows.Close()

	foundKeys := 0
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			log.Printf("错误: [AdminConfigService DB] 扫描IP速率限制配置失败: %v", err)
			return nil, fmt.Errorf("数据库扫描失败")
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
		log.Printf("警告: [AdminConfigService DB] 未能完整获取IP速率限制配置，可能缺少默认值。")
		return nil, nil // 返回nil表示未找到或不完整
	}
	return settings, nil
}

// UpdateIPLimitSettings 更新全局IP速率限制配置
func (s *AdminConfigServiceImpl) UpdateIPLimitSettings(ctx context.Context, settings IPLimitSetting) (err error) {
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
func (s *AdminConfigServiceImpl) GetUserLimitSettings(ctx context.Context, userID int64) (*UserLimitSetting, error) {
	var rateLimit sql.NullFloat64
	var burstSize sql.NullInt64

	// 注意：这里的 'users' 表是由 aegauth 包管理的，但我们在这里通过共享的 *sql.DB 连接来查询它。
	query := "SELECT rate_limit_per_second, burst_size FROM users WHERE id = ?"
	err := s.db.QueryRowContext(ctx, query, userID).Scan(&rateLimit, &burstSize)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // 用户存在但未找到，不是错误
		}
		log.Printf("错误: [AdminConfigService DB] 查询用户(ID: %d)速率限制失败: %v", userID, err)
		return nil, fmt.Errorf("数据库查询失败")
	}

	// 只有当数据库中明确设置了值时，才返回配置
	if !rateLimit.Valid || !burstSize.Valid {
		return nil, nil
	}

	return &UserLimitSetting{
		RateLimitPerSecond: rateLimit.Float64,
		BurstSize:          int(burstSize.Int64),
	}, nil
}

// UpdateUserLimitSettings 更新特定用户的速率限制配置
func (s *AdminConfigServiceImpl) UpdateUserLimitSettings(ctx context.Context, userID int64, settings UserLimitSetting) error {
	query := "UPDATE users SET rate_limit_per_second = ?, burst_size = ? WHERE id = ?"
	result, err := s.db.ExecContext(ctx, query, settings.RateLimitPerSecond, settings.BurstSize, userID)
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] 更新用户(ID: %d)速率限制失败: %v", userID, err)
		return fmt.Errorf("数据库更新失败")
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("用户ID %d 不存在", userID)
	}
	return nil
}

// GetBizRateLimitSettings 获取特定业务组的速率限制配置
func (s *AdminConfigServiceImpl) GetBizRateLimitSettings(ctx context.Context, bizName string) (*BizRateLimitSetting, error) {
	query := "SELECT rate_limit_per_second, burst_size FROM biz_ratelimit_settings WHERE biz_name = ?"
	setting := &BizRateLimitSetting{}
	err := s.db.QueryRowContext(ctx, query, bizName).Scan(&setting.RateLimitPerSecond, &setting.BurstSize)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // 未找到该业务组的特定配置，不是错误
		}
		log.Printf("错误: [AdminConfigService DB] 查询业务组 '%s' 速率限制失败: %v", bizName, err)
		return nil, fmt.Errorf("数据库查询失败")
	}
	return setting, nil
}

// UpdateBizRateLimitSettings 更新特定业务组的速率限制配置
func (s *AdminConfigServiceImpl) UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings BizRateLimitSetting) error {
	query := `
        INSERT INTO biz_ratelimit_settings (biz_name, rate_limit_per_second, burst_size) 
        VALUES (?, ?, ?) 
        ON CONFLICT(biz_name) DO UPDATE SET 
            rate_limit_per_second = excluded.rate_limit_per_second, 
            burst_size = excluded.burst_size`
	_, err := s.db.ExecContext(ctx, query, bizName, settings.RateLimitPerSecond, settings.BurstSize)
	if err != nil {
		log.Printf("错误: [AdminConfigService DB] 更新业务组 '%s' 速率限制失败: %v", bizName, err)
		return fmt.Errorf("数据库更新失败")
	}
	return nil
}

// =======================================================

// InvalidateCacheForBiz 用于在管理员更新配置后，手动使指定业务组的缓存失效。
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
