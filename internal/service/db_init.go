// file: internal/service/db_init.go
package service

import (
	"database/sql"
	"fmt"
	"log"
)

// InitPlatformTables 负责在系统启动时，检查并创建/更新所有平台级的系统管理表。
func InitPlatformTables(db *sql.DB) error {
	if err := initUserTable(db); err != nil {
		return fmt.Errorf("初始化用户表失败: %w", err)
	}
	if err := initPermissionTables(db); err != nil {
		return fmt.Errorf("初始化权限表失败: %w", err)
	}
	if err := initOperationLogTable(db); err != nil {
		return fmt.Errorf("初始化操作日志表失败: %w", err)
	}
	if err := initGlobalSettingsTable(db); err != nil {
		return fmt.Errorf("初始化全局设置表失败: %w", err)
	}

	// ✅ NEW: 调用新函数来初始化插件管理相关的表
	if err := initPluginManagementTable(db); err != nil {
		return fmt.Errorf("初始化插件管理表失败: %w", err)
	}

	log.Println("✅ 数据库: 所有系统表结构初始化/检查完成。")
	return nil
}

// initUserTable 创建用户表
func initUserTable(db *sql.DB) error {
	query := `
    CREATE TABLE IF NOT EXISTS _user(
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        username TEXT UNIQUE NOT NULL,
        password_hash TEXT NOT NULL,
        role TEXT NOT NULL,
        rate_limit_per_second REAL, -- for user-specific rate limiting
        burst_size INTEGER
    );`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("创建 '_user' 表失败: %w", err)
	}
	// 为常用查询创建索引
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_user_username ON _user (username);`)
	return err
}

// initPermissionTables 创建或更新所有与权限配置相关的表
func initPermissionTables(db *sql.DB) error {
	// 创建业务组元数据表
	queryBizOverall := `
    CREATE TABLE IF NOT EXISTS biz_overall_settings (
        biz_name TEXT PRIMARY KEY,
        is_publicly_searchable BOOLEAN DEFAULT TRUE NOT NULL,
        default_query_table TEXT
    );`
	if _, err := db.Exec(queryBizOverall); err != nil {
		return fmt.Errorf("创建 'biz_overall_settings' 表失败: %w", err)
	}

	// 创建表级权限配置表 (包含新的写权限字段)
	queryTablePerms := `
    CREATE TABLE IF NOT EXISTS biz_searchable_tables (
        biz_name TEXT NOT NULL,
        table_name TEXT NOT NULL,
        allow_create BOOLEAN DEFAULT FALSE NOT NULL,
        allow_update BOOLEAN DEFAULT FALSE NOT NULL,
        allow_delete BOOLEAN DEFAULT FALSE NOT NULL,
        PRIMARY KEY (biz_name, table_name),
        FOREIGN KEY (biz_name) REFERENCES biz_overall_settings(biz_name) ON DELETE CASCADE
    );`
	if _, err := db.Exec(queryTablePerms); err != nil {
		return fmt.Errorf("创建 'biz_searchable_tables' 表失败: %w", err)
	}

	// 创建字段级权限配置表
	queryFieldPerms := `
    CREATE TABLE IF NOT EXISTS biz_table_field_settings (
        biz_name TEXT NOT NULL,
        table_name TEXT NOT NULL,
        field_name TEXT NOT NULL,
        is_searchable BOOLEAN DEFAULT FALSE NOT NULL,
        is_returnable BOOLEAN DEFAULT FALSE NOT NULL,
        data_type TEXT DEFAULT 'string' NOT NULL,
        PRIMARY KEY (biz_name, table_name, field_name),
        FOREIGN KEY (biz_name, table_name) REFERENCES biz_searchable_tables(biz_name, table_name) ON DELETE CASCADE
    );`
	if _, err := db.Exec(queryFieldPerms); err != nil {
		return fmt.Errorf("创建 'biz_table_field_settings' 表失败: %w", err)
	}

	// 创建视图定义表
	queryViewDefs := `
	CREATE TABLE IF NOT EXISTS biz_view_definitions (
		biz_name TEXT NOT NULL,
		table_name TEXT NOT NULL,
		view_name TEXT NOT NULL,
		view_config_json TEXT NOT NULL,
		is_default BOOLEAN DEFAULT FALSE NOT NULL,
		PRIMARY KEY (biz_name, table_name, view_name)
	);`
	if _, err := db.Exec(queryViewDefs); err != nil {
		return fmt.Errorf("创建 'biz_view_definitions' 表失败: %w", err)
	}

	return nil
}

// initOperationLogTable 创建操作日志表，用于实现回滚功能
func initOperationLogTable(db *sql.DB) error {
	query := `
    CREATE TABLE IF NOT EXISTS operation_log (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        operation_id TEXT NOT NULL UNIQUE,
        timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
        user_id INTEGER,
        biz_name TEXT NOT NULL,
        table_name TEXT NOT NULL,
        operation_type TEXT NOT NULL, -- 'CREATE', 'UPDATE', 'DELETE'
        target_pk TEXT NOT NULL,
        data_before TEXT, -- 操作前的数据快照 (JSON)
        data_after TEXT,  -- 操作后的数据快照 (JSON)
        status TEXT NOT NULL -- 'COMPLETED', 'ROLLED_BACK'
    );`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("创建 'operation_log' 表失败: %w", err)
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_oplog_op_id ON operation_log(operation_id);`)
	return err
}

// initGlobalSettingsTable 创建全局设置和速率限制相关的表
func initGlobalSettingsTable(db *sql.DB) error {
	queryGlobal := `
	CREATE TABLE IF NOT EXISTS global_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		description TEXT,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(queryGlobal); err != nil {
		return fmt.Errorf("创建 'global_settings' 表失败: %w", err)
	}
	// 插入默认的IP速率限制值，如果不存在的话
	insertGlobal := `
	INSERT OR IGNORE INTO global_settings (key, value, description) VALUES
		('ip_rate_limit_per_minute', '60', '未认证IP的默认每分钟请求数'),
		('ip_burst_size', '20', '未认证IP的默认瞬时请求峰值');`
	if _, err := db.Exec(insertGlobal); err != nil {
		return fmt.Errorf("插入默认全局设置失败: %w", err)
	}

	queryBizRateLimit := `
	CREATE TABLE IF NOT EXISTS biz_ratelimit_settings (
		biz_name TEXT PRIMARY KEY,
		rate_limit_per_second REAL NOT NULL DEFAULT 5.0,
		burst_size INTEGER NOT NULL DEFAULT 10,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(queryBizRateLimit); err != nil {
		return fmt.Errorf("创建 'biz_ratelimit_settings' 表失败: %w", err)
	}

	return nil
}

// initPluginManagementTable 创建用于存储插件状态和实例配置的表。
func initPluginManagementTable(db *sql.DB) error {
	// 已安装插件表
	queryInstalled := `
    CREATE TABLE IF NOT EXISTS installed_plugins (
        plugin_id TEXT NOT NULL,
        version TEXT NOT NULL,
        install_path TEXT NOT NULL,
        installed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        PRIMARY KEY (plugin_id, version)
    );`
	if _, err := db.Exec(queryInstalled); err != nil {
		return fmt.Errorf("创建 'installed_plugins' 表失败: %w", err)
	}

	queryInstances := `
	CREATE TABLE IF NOT EXISTS plugin_instances (
		instance_id TEXT PRIMARY KEY,
		display_name TEXT NOT NULL,
		plugin_id TEXT NOT NULL,
		version TEXT NOT NULL,
		biz_name TEXT NOT NULL UNIQUE, -- 一个实例只服务一个业务组，且业务组不能重复
		port INTEGER NOT NULL UNIQUE,    -- 每个实例拥有独立的端口号
		status TEXT NOT NULL DEFAULT 'STOPPED', -- 状态: STOPPED, RUNNING, ERROR
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_started_at DATETIME,
		FOREIGN KEY (plugin_id, version) REFERENCES installed_plugins(plugin_id, version)
	);`
	if _, err := db.Exec(queryInstances); err != nil {
		return fmt.Errorf("创建 'plugin_instances' 表失败: %w", err)
	}

	return nil
}
