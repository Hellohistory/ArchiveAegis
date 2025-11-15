// Package main 提供 SQLite 插件的主程序入口
// 文件位置: cmd/plugins/sqlite_plugin/main.go
package main

import (
	"ArchiveAegis/internal/adapter/datasource/sqlite"
	"ArchiveAegis/internal/service/admin_config"
	"ArchiveAegis/pkg/go_plugin_sdk"
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed README.md
var pluginDescription string // 插件说明文档内容

// main 为插件服务的入口函数，定义插件元信息与初始化逻辑
func main() {
	// 定义插件元信息
	pluginInfo := go_plugin_sdk.PluginInfo{
		Name:                  "sqlite-plugin",         // 插件名称
		Version:               "1.0.0",                 // 插件版本
		Type:                  "SQL",                   // 插件类型
		DescriptionMarkdown:   pluginDescription,       // 插件说明
		SupportedCapabilities: []string{"AGGREGATION"}, // 支持的能力标识
		Meta: go_plugin_sdk.PluginMeta{
			SupportedProtocolVersion: "1.0.0",
		},
	}

	// 定义插件初始化逻辑，返回插件实例
	initializer := func(ctx context.Context, cfg go_plugin_sdk.PluginConfig) (go_plugin_sdk.Plugin, error) {
		slog.Info("正在初始化 SQLite 插件核心依赖...")

		authDbPath := filepath.Join(cfg.InstanceDir, "auth.db")
		pluginSysDB, err := initAuthDB(authDbPath)
		if err != nil {
			return nil, fmt.Errorf("插件无法初始化认证数据库连接: %w", err)
		}

		adminConfigService, err := admin_config.NewAdminConfigServiceImpl(pluginSysDB, 100, 1*time.Minute)
		if err != nil {
			_ = pluginSysDB.Close()
			return nil, fmt.Errorf("插件无法创建 AdminConfigService: %w", err)
		}

		sqliteManager := sqlite.NewManager(adminConfigService, pluginSysDB)

		if err := sqliteManager.InitForBiz(context.Background(), cfg.InstanceDir, cfg.BizName); err != nil {
			_ = pluginSysDB.Close()
			return nil, fmt.Errorf("插件初始化业务失败: %w", err)
		}
		slog.Info("成功初始化业务数据", "biz", cfg.BizName)

		return sqliteManager, nil
	}

	// 启动插件服务
	if err := go_plugin_sdk.Serve(pluginInfo, initializer); err != nil {
		slog.Error("插件服务启动失败", "error", err)
		os.Exit(1)
	}
}

// initAuthDB 初始化 SQLite 数据库连接并进行连接验证
func initAuthDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=ON&_synchronous=NORMAL", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开/创建认证数据库 '%s' 失败: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接认证数据库 '%s' (Ping) 失败: %w", path, err)
	}
	return db, nil
}
