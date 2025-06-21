// file: cmd/gateway/main.go

package main

import (
	"ArchiveAegis/internal/aegmiddleware"
	"ArchiveAegis/internal/aegobserve"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"ArchiveAegis/internal/service/admin_config"
	"ArchiveAegis/internal/service/plugin_manager"
	"ArchiveAegis/internal/transport/http/router"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/viper"
	_ "modernc.org/sqlite"
)

const version = "v1.0.0-alpha5"

// =============================================================================
// 配置与应用核心结构体
// =============================================================================

type PluginManagementConfig struct {
	InstallDirectory string                            `mapstructure:"install_directory"`
	Repositories     []plugin_manager.RepositoryConfig `mapstructure:"repositories"`
}

type ServerConfig struct {
	Port     int    `mapstructure:"port"`
	LogLevel string `mapstructure:"log_level"`
}

type Config struct {
	Server           ServerConfig           `mapstructure:"server"`
	PluginManagement PluginManagementConfig `mapstructure:"plugin_management"`
}

// application 结构体作为我们应用的核心容器，持有所有依赖。
type application struct {
	config             Config
	db                 *sql.DB
	logger             *slog.Logger
	pluginManager      *plugin_manager.PluginManager
	adminConfigService port.QueryAdminConfigService
	rateLimiter        *aegmiddleware.BusinessRateLimiter
	dataSourceRegistry map[string]port.DataSource
	closableAdapters   *[]io.Closer
}

// =============================================================================
// 主程序入口与生命周期管理
// =============================================================================

func main() {
	// build 函数负责创建和初始化 application 实例
	app, err := build()
	if err != nil {
		// 如果在 build 阶段就出错，此时 slog 可能还未初始化，使用标准 log
		log.Fatalf("CRITICAL: 应用初始化失败: %v", err)
	}

	// 确保数据库连接在程序退出时被关闭
	defer func() {
		app.logger.Info("正在关闭系统数据库连接...")
		if err := app.db.Close(); err != nil {
			app.logger.Error("关闭系统数据库时发生错误", "error", err)
		}
	}()

	// app.run 负责运行应用
	if err := app.run(); err != nil {
		app.logger.Error("应用运行时发生错误", "error", err)
		os.Exit(1)
	}

	app.logger.Info("程序已成功退出。")
}

// build 函数封装了所有的初始化逻辑
func build() (*application, error) {
	// --- 命令行标志处理 ---
	serviceTokenUser := flag.String("gen-service-token", "", "为指定的服务账户用户名生成一个长生命周期的Token并退出")
	flag.Parse()

	// --- 配置加载 ---
	log.Printf("ArchiveAegis Universal Kernel %s 正在启动...", version)
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("无法获取可执行文件路径: %w", err)
	}
	rootDir := filepath.Dir(filepath.Dir(exePath))
	configFilePath := filepath.Join(rootDir, "configs", "config.yaml")
	viper.SetConfigFile(configFilePath)
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件 '%s' 失败: %w", configFilePath, err)
	}
	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("解析配置到结构体失败: %w", err)
	}

	// --- 数据库和可观测性初始化 ---
	instanceDir := filepath.Join(rootDir, "instance")
	if _, err := os.Stat(instanceDir); os.IsNotExist(err) {
		_ = os.MkdirAll(instanceDir, 0755)
	}
	authDbPath := filepath.Join(instanceDir, "auth.db")
	sysDB, err := initAuthDB(authDbPath)
	if err != nil {
		return nil, err
	}

	if err := service.InitPlatformTables(sysDB); err != nil {
		return nil, err
	}

	// 如果是生成 Token 的命令，则执行并退出
	if *serviceTokenUser != "" {
		// 这里返回的 error 会被 main 捕获并处理
		return nil, generateServiceTokenAndExit(sysDB, *serviceTokenUser)
	}

	enabledFeatures, err := loadEnabledFeatures(sysDB)
	if err != nil {
		return nil, err
	}

	if enabledFeatures["io.archiveaegis.system.observability"] {
		aegobserve.InitLogger(config.Server.LogLevel)
	} else {
		log.Println("ℹ️  高级可观测性功能未启用，使用标准日志。")
	}

	slog.Info("ArchiveAegis Universal Kernel starting up", "version", version)

	// --- 服务初始化 ---
	config.PluginManagement.InstallDirectory = filepath.Join(rootDir, config.PluginManagement.InstallDirectory)
	for i, repo := range config.PluginManagement.Repositories {
		if !strings.Contains(repo.URL, "://") {
			absPath := filepath.Join(rootDir, repo.URL)
			config.PluginManagement.Repositories[i].URL = "file://" + filepath.ToSlash(absPath)
		}
	}

	adminConfigService, err := admin_config.NewAdminConfigServiceImpl(sysDB, 1000, 5*time.Minute)
	if err != nil {
		return nil, err
	}

	dataSourceRegistry := make(map[string]port.DataSource)
	closableAdapters := make([]io.Closer, 0)
	pm, err := plugin_manager.NewPluginManager(sysDB, rootDir, config.PluginManagement.Repositories, config.PluginManagement.InstallDirectory, dataSourceRegistry, &closableAdapters)
	if err != nil {
		return nil, err
	}

	rateLimiter := aegmiddleware.NewBusinessRateLimiter(adminConfigService, 10, 30)

	// --- 按需启用监控 ---
	if enabledFeatures["io.archiveaegis.system.observability"] {
		aegobserve.EnablePprof("0.0.0.0:6060")
	}
	aegobserve.Register()
	slog.Info("监控: metrics 已注册。")

	// --- 组装 application 实例 ---
	app := &application{
		config:             config,
		db:                 sysDB,
		logger:             slog.Default(),
		pluginManager:      pm,
		adminConfigService: adminConfigService,
		rateLimiter:        rateLimiter,
		dataSourceRegistry: dataSourceRegistry,
		closableAdapters:   &closableAdapters,
	}

	return app, nil
}

// run 方法负责启动 HTTP 服务和处理优雅停机。
func (app *application) run() error {
	// 启动后台任务
	app.pluginManager.RefreshRepositories()
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			app.pluginManager.RefreshRepositories()
		}
	}()
	app.logger.Info("后台任务: 插件仓库定期刷新已启动。")

	// 准备 Setup Token
	var setupToken string
	var setupTokenDeadline time.Time
	if service.UserCount(app.db) == 0 {
		setupToken = genToken()
		setupTokenDeadline = time.Now().Add(30 * time.Minute)
		app.logger.Warn("系统中无管理员，安装令牌已生成 (30分钟内有效)", "setup_token", setupToken)
	}

	// 创建 HTTP 路由器
	httpRouter := router.New(
		router.Dependencies{
			Registry:           app.dataSourceRegistry,
			AdminConfigService: app.adminConfigService,
			PluginManager:      app.pluginManager,
			RateLimiter:        app.rateLimiter,
			AuthDB:             app.db,
			SetupToken:         setupToken,
			SetupTokenDeadline: setupTokenDeadline,
		},
	)
	app.logger.Info("传输层: HTTP 路由器创建完成。")

	// 创建并启动 HTTP 服务
	addr := fmt.Sprintf(":%d", app.config.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: httpRouter,
	}

	shutdownErr := make(chan error)

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		app.logger.Info("收到停机信号，准备优雅关闭...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		app.logger.Info("正在关闭所有插件适配器...")
		for _, closer := range *app.closableAdapters {
			if err := closer.Close(); err != nil {
				app.logger.Error("关闭适配器时发生错误", "error", err)
			}
		}

		shutdownErr <- server.Shutdown(ctx)
	}()

	app.logger.Info("ArchiveAegis 内核启动成功，开始监听HTTP请求...", "address", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	if err := <-shutdownErr; err != nil {
		return err
	}

	app.logger.Info("HTTP服务已成功关闭。")
	return nil
}

// =============================================================================
// 辅助函数
// =============================================================================

// generateServiceTokenAndExit 处理生成Token的逻辑并退出。
func generateServiceTokenAndExit(db *sql.DB, username string) error {
	id, role, ok := service.GetUserByUsername(db, username)
	if !ok {
		log.Printf("服务账户 '%s' 不存在，将自动创建...", username)
		var createErr error
		id, role, createErr = service.CreateServiceAccount(db, username)
		if createErr != nil {
			return fmt.Errorf("自动创建服务账户 '%s' 失败: %w", username, createErr)
		}
	} else {
		log.Printf("服务账户 '%s' 已存在 (ID: %d)，为其生成新Token...", username, id)
	}

	token, err := service.GenServiceToken(id, role)
	if err != nil {
		return fmt.Errorf("生成服务Token失败: %w", err)
	}

	fmt.Printf("\n为服务账户 '%s' (role: %s, id: %d) 生成的Token:\n", username, role, id)
	fmt.Println("------------------------------------------------------------------")
	fmt.Println(token)
	fmt.Println("------------------------------------------------------------------")
	fmt.Println("请将此Token配置到你的 Prometheus 或其他服务中。")

	os.Exit(0)
	return nil // 实际上，os.Exit(0)会立刻终止程序
}

// loadEnabledFeatures 从数据库加载启用的功能列表
func loadEnabledFeatures(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("SELECT feature_id FROM system_features WHERE enabled = TRUE")
	if err != nil {
		return nil, fmt.Errorf("查询启用的系统功能列表失败: %w", err)
	}
	defer rows.Close()

	features := make(map[string]bool)
	for rows.Next() {
		var featureID string
		if err := rows.Scan(&featureID); err != nil {
			log.Printf("⚠️ 扫描启用的功能ID失败: %v", err)
			continue
		}
		features[featureID] = true
	}
	return features, rows.Err()
}

// initAuthDB 封装了认证数据库的初始化逻辑
func initAuthDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=ON&_synchronous=NORMAL", path)
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

// genToken 生成一次性的安装令牌
func genToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback_token_generation_failed"
	}
	return hex.EncodeToString(b)
}
