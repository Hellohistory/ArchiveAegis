// file: cmd/plugins/sqlite_plugin/main.go
package main

import (
	"ArchiveAegis/internal/adapter/datasource/sqlite"
	"ArchiveAegis/internal/service/admin_config"
	"ArchiveAegis/pkg/go_plugin_sdk" // <-- 1. 引入新创建的 SDK
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	// 移除了大量不再需要的 import
	_ "modernc.org/sqlite"
)

//go:embed README.md
var pluginDescription string

// main 函数现在变得极其简洁和声明式
func main() {
	// 2. 定义插件的静态元数据
	pluginInfo := go_plugin_sdk.PluginInfo{
		Name:                  "sqlite-plugin", // 这是默认名称，可被 --name flag 覆盖
		Version:               "1.0.0",         // 版本号现在在这里定义
		Type:                  "SQL",
		DescriptionMarkdown:   pluginDescription,
		SupportedCapabilities: []string{"AGGREGATION"},
	}

	// 3. 定义初始化逻辑，它是一个函数，负责创建插件实例
	initializer := func(ctx context.Context, cfg go_plugin_sdk.PluginConfig) (go_plugin_sdk.Plugin, error) {
		slog.Info("正在初始化 SQLite 插件核心依赖...")

		// 所有旧 main 函数中的依赖初始化代码都移到这里
		authDbPath := filepath.Join(cfg.InstanceDir, "auth.db")
		pluginSysDB, err := initAuthDB(authDbPath)
		if err != nil {
			return nil, fmt.Errorf("插件无法初始化认证数据库连接: %w", err)
		}
		// 注意: 数据库的关闭 (pluginSysDB.Close()) 需要更完善的管理。
		// 一个好的实践是在插件逻辑的实现中包含一个 Stop() 方法，
		// SDK 在未来可以支持调用它以实现优雅关闭。
		// 在 alpha 阶段，我们暂时忽略它。

		adminConfigService, err := admin_config.NewAdminConfigServiceImpl(pluginSysDB, 100, 1*time.Minute)
		if err != nil {
			return nil, fmt.Errorf("插件无法创建 AdminConfigService: %w", err)
		}

		// sqlite.NewManager 返回的实例已经满足我们设计的 go_plugin_sdk.Plugin 接口！
		sqliteManager := sqlite.NewManager(adminConfigService)
		if err := sqliteManager.InitForBiz(context.Background(), cfg.InstanceDir, cfg.BizName); err != nil {
			return nil, fmt.Errorf("插件初始化业务失败: %w", err)
		}
		slog.Info("成功初始化业务数据", "biz", cfg.BizName)

		return sqliteManager, nil
	}

	// 4. 将所有东西交给 SDK，启动服务！
	// 这一行代码替换了所有旧的 flag 解析、gRPC 服务器设置和启动代码。
	if err := go_plugin_sdk.Serve(pluginInfo, initializer); err != nil {
		slog.Error("插件服务启动失败", "error", err)
		os.Exit(1)
	}
}

// initAuthDB 辅助函数保持不变，无需改动
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
