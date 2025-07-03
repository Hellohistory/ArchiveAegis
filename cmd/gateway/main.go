// Package main 为 ArchiveAegis 项目提供服务网关的主程序入口
// 文件位置: cmd/gateway/main.go

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

const version = "v1.0.0-alpha10"

// =============================================================================
// 配置与应用核心结构体
// =============================================================================

// PluginManagementConfig 描述插件管理的配置项，包括安装目录和插件仓库列表
type PluginManagementConfig struct {
	InstallDirectory string                            `mapstructure:"install_directory"` // 插件安装路径
	Repositories     []plugin_manager.RepositoryConfig `mapstructure:"repositories"`      // 插件仓库配置列表
}

// ServerConfig 描述 HTTP 服务的运行参数配置
type ServerConfig struct {
	Port     int    `mapstructure:"port"`      // 服务监听端口
	LogLevel string `mapstructure:"log_level"` // 日志级别
}

// Config 表示主配置文件的结构，包含服务与插件管理配置
type Config struct {
	Server           ServerConfig           `mapstructure:"server"`            // HTTP 服务配置
	PluginManagement PluginManagementConfig `mapstructure:"plugin_management"` // 插件管理配置
}

// application 表示服务应用的核心结构体，聚合了所有运行时依赖
type application struct {
	config             Config                             // 应用配置
	db                 *sql.DB                            // 系统数据库实例
	logger             *slog.Logger                       // 日志记录器
	pluginManager      *plugin_manager.PluginManager      // 插件管理器
	adminConfigService port.QueryAdminConfigService       // 管理配置服务接口
	rateLimiter        *aegmiddleware.BusinessRateLimiter // 业务限流器
	executorRegistry   map[string]port.Executor           // 执行器注册表（按业务名映射）
	closableAdapters   *[]io.Closer                       // 所有可关闭的资源集合
}

// =============================================================================
// 主程序入口与生命周期管理
// =============================================================================

// main 为服务的程序入口，负责构建应用实例并启动服务流程
func main() {
	app, err := build()
	if err != nil {
		log.Fatalf("CRITICAL: 应用初始化失败: %v", err)
	}
	defer func() {
		app.logger.Info("正在关闭系统数据库连接...")
		if err := app.db.Close(); err != nil {
			app.logger.Error("关闭系统数据库时发生错误", "error", err)
		}
	}()
	if err := app.run(); err != nil {
		app.logger.Error("应用运行时发生错误", "error", err)
		os.Exit(1)
	}
	app.logger.Info("程序已成功退出。")
}

// build 负责初始化配置、数据库、服务组件等运行时依赖，返回完整应用实例
func build() (*application, error) {
	// 处理命令行标志参数（用于生成服务账户 Token）
	serviceTokenUser := flag.String("gen-service-token", "", "为指定的服务账户用户名生成一个长生命周期的Token并退出")
	flag.Parse()

	// 加载配置文件
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

	// 初始化认证数据库
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

	// 若为生成 Token 操作，则执行并退出
	if *serviceTokenUser != "" {
		return nil, generateServiceTokenAndExit(sysDB, *serviceTokenUser)
	}

	// 加载已启用的系统功能
	enabledFeatures, err := loadEnabledFeatures(sysDB)
	if err != nil {
		return nil, err
	}

	// 初始化日志系统
	if enabledFeatures["io.archiveaegis.system.observability"] {
		aegobserve.InitLogger(config.Server.LogLevel)
	} else {
		log.Println("ℹ️  高级可观测性功能未启用，使用标准日志。")
	}
	slog.Info("ArchiveAegis Universal Kernel starting up", "version", version)

	// 转换插件路径与仓库地址为绝对路径
	config.PluginManagement.InstallDirectory = filepath.Join(rootDir, config.PluginManagement.InstallDirectory)
	for i, repo := range config.PluginManagement.Repositories {
		if !strings.Contains(repo.URL, "://") {
			absPath := filepath.Join(rootDir, repo.URL)
			config.PluginManagement.Repositories[i].URL = "file://" + filepath.ToSlash(absPath)
		}
	}

	// 初始化服务组件
	adminConfigService, err := admin_config.NewAdminConfigServiceImpl(sysDB, 1000, 5*time.Minute)
	if err != nil {
		return nil, err
	}

	executorRegistry := make(map[string]port.Executor)
	closableAdapters := make([]io.Closer, 0)

	pm, err := plugin_manager.NewPluginManager(
		sysDB,
		rootDir,
		config.PluginManagement.Repositories,
		config.PluginManagement.InstallDirectory,
		executorRegistry,
		&closableAdapters,
	)
	if err != nil {
		return nil, err
	}

	rateLimiter := aegmiddleware.NewBusinessRateLimiter(adminConfigService, 10, 30)

	// 启用性能分析服务
	if enabledFeatures["io.archiveaegis.system.observability"] {
		aegobserve.EnablePprof("0.0.0.0:6060")
	}
	slog.Info("监控: metrics 已通过包初始化自动注册。")

	// 构建 application 实例
	app := &application{
		config:             config,
		db:                 sysDB,
		logger:             slog.Default(),
		pluginManager:      pm,
		adminConfigService: adminConfigService,
		rateLimiter:        rateLimiter,
		executorRegistry:   executorRegistry,
		closableAdapters:   &closableAdapters,
	}
	return app, nil
}

// run 启动 HTTP 服务，注册路由，执行后台任务，并监听系统信号实现优雅停机
func (app *application) run() error {
	// 启动插件仓库定时刷新任务
	app.pluginManager.RefreshRepositories()
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			app.pluginManager.RefreshRepositories()
		}
	}()
	app.logger.Info("后台任务: 插件仓库定期刷新已启动。")

	// 如系统中无用户，生成临时 Setup Token
	var setupToken string
	var setupTokenDeadline time.Time
	if service.UserCount(app.db) == 0 {
		setupToken = genToken()
		setupTokenDeadline = time.Now().Add(30 * time.Minute)
		app.logger.Warn("系统中无管理员，安装令牌已生成 (30分钟内有效)", "setup_token", setupToken)
	}

	// 创建 HTTP 路由器并注入依赖
	httpRouter := router.New(
		router.Dependencies{
			Registry:           app.executorRegistry,
			AdminConfigService: app.adminConfigService,
			PluginManager:      app.pluginManager,
			RateLimiter:        app.rateLimiter,
			AuthDB:             app.db,
			SetupToken:         setupToken,
			SetupTokenDeadline: setupTokenDeadline,
		},
	)
	app.logger.Info("传输层: HTTP 路由器创建完成。")

	// 启动 HTTP 服务
	addr := fmt.Sprintf(":%d", app.config.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: httpRouter,
	}

	shutdownErr := make(chan error)

	// 启动系统信号监听与优雅关闭逻辑
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

// generateServiceTokenAndExit 为指定服务账户生成访问令牌，并将其输出后终止程序
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
	return nil
}

// loadEnabledFeatures 从数据库中加载所有已启用的系统功能标识符
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

// initAuthDB 初始化 SQLite 数据库连接，并进行连接验证
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

// genToken 生成长度为 16 字节的随机令牌字符串
func genToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback_token_generation_failed"
	}
	return hex.EncodeToString(b)
}
