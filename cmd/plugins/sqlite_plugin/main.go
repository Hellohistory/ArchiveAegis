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
var pluginDescription string

// main 函数现在变得极其简洁和声明式
func main() {
	// 定义插件的静态元数据
	pluginInfo := go_plugin_sdk.PluginInfo{
		Name:                  "sqlite-plugin",
		Version:               "1.0.0",
		Type:                  "SQL",
		DescriptionMarkdown:   pluginDescription,
		SupportedCapabilities: []string{"AGGREGATION"},
	}

	// 定义初始化逻辑，它是一个函数，负责创建插件实例
	initializer := func(ctx context.Context, cfg go_plugin_sdk.PluginConfig) (go_plugin_sdk.Plugin, error) {
		slog.Info("正在初始化 SQLite 插件核心依赖...")

		authDbPath := filepath.Join(cfg.InstanceDir, "auth.db")
		pluginSysDB, err := initAuthDB(authDbPath)
		if err != nil {
			return nil, fmt.Errorf("插件无法初始化认证数据库连接: %w", err)
		}
		// 数据库连接现在会被传递给 Manager，由其生命周期管理

		adminConfigService, err := admin_config.NewAdminConfigServiceImpl(pluginSysDB, 100, 1*time.Minute)
		if err != nil {
			_ = pluginSysDB.Close() // 如果后续步骤失败，需要在这里关闭数据库连接
			return nil, fmt.Errorf("插件无法创建 AdminConfigService: %w", err)
		}

		sqliteManager := sqlite.NewManager(adminConfigService, pluginSysDB)

		if err := sqliteManager.InitForBiz(context.Background(), cfg.InstanceDir, cfg.BizName); err != nil {
			_ = pluginSysDB.Close() // 如果后续步骤失败，也需要在这里关闭数据库连接
			return nil, fmt.Errorf("插件初始化业务失败: %w", err)
		}
		slog.Info("成功初始化业务数据", "biz", cfg.BizName)

		return sqliteManager, nil
	}

	// 这一行代码替换了所有旧的 flag 解析、gRPC 服务器设置和启动代码。
	if err := go_plugin_sdk.Serve(pluginInfo, initializer); err != nil {
		slog.Error("插件服务启动失败", "error", err)
		os.Exit(1)
	}
}

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
