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
	"fmt"
	"github.com/spf13/viper"
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

	_ "modernc.org/sqlite"
)

const version = "v1.0.0-alpha5"

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
			// 在关键初始化阶段，使用标准log
			log.Printf("⚠️ 扫描启用的功能ID失败: %v", err)
			continue
		}
		features[featureID] = true
	}
	return features, rows.Err()
}

func main() {
	// 在日志系统完全初始化前，使用标准 log
	log.Printf("ArchiveAegis Universal Kernel %s 正在启动...", version)

	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("CRITICAL: 无法获取可执行文件路径: %v", err)
	}
	rootDir := filepath.Dir(filepath.Dir(exePath))

	configFilePath := filepath.Join(rootDir, "configs", "config.yaml")
	viper.SetConfigFile(configFilePath)

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("CRITICAL: 读取配置文件 '%s' 失败: %v", configFilePath, err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("CRITICAL: 解析配置到结构体失败: %v", err)
	}

	instanceDir := filepath.Join(rootDir, "instance")
	if _, err := os.Stat(instanceDir); os.IsNotExist(err) {
		_ = os.MkdirAll(instanceDir, 0755)
	}
	authDbPath := filepath.Join(instanceDir, "auth.db")
	sysDB, err := initAuthDB(authDbPath)
	if err != nil {
		log.Fatalf("CRITICAL: 初始化认证数据库失败: %v", err)
	}
	defer func() {
		slog.Info("正在关闭系统数据库连接...")
		if err := sysDB.Close(); err != nil {
			slog.Error("关闭系统数据库时发生错误", "error", err)
		}
	}()

	// 确保表结构存在
	if err := service.InitPlatformTables(sysDB); err != nil {
		log.Fatalf("CRITICAL: 初始化平台系统表失败: %v", err)
	}

	// 加载功能开关
	enabledFeatures, err := loadEnabledFeatures(sysDB)
	if err != nil {
		log.Fatalf("CRITICAL: 加载系统功能开关失败: %v", err)
	}

	// 根据开关决定日志和 pprof 的初始化
	if enabledFeatures["io.archiveaegis.system.observability"] {
		aegobserve.InitLogger(config.Server.LogLevel) // 使用 slog
	} else {
		log.Println("ℹ️  高级可观测性功能未启用，使用标准日志。")
	}

	slog.Info("ArchiveAegis Universal Kernel starting up", "version", version)
	slog.Info("检测到项目根目录", "path", rootDir)
	slog.Info("配置加载并解析成功", "path", configFilePath)

	config.PluginManagement.InstallDirectory = filepath.Join(rootDir, config.PluginManagement.InstallDirectory)
	slog.Info("插件安装目录绝对路径", "path", config.PluginManagement.InstallDirectory)

	for i, repo := range config.PluginManagement.Repositories {
		if !strings.Contains(repo.URL, "://") {
			absPath := filepath.Join(rootDir, repo.URL)
			config.PluginManagement.Repositories[i].URL = "file://" + filepath.ToSlash(absPath)
			slog.Info("仓库URL已转换为绝对路径", "repo", repo.Name, "url", config.PluginManagement.Repositories[i].URL)
		}
	}

	adminConfigService, err := admin_config.NewAdminConfigServiceImpl(sysDB, 1000, 5*time.Minute)
	if err != nil {
		slog.Error("初始化 AdminConfigService 失败", "error", err)
		os.Exit(1)
	}
	slog.Info("服务层: AdminConfigService 初始化完成")

	dataSourceRegistry := make(map[string]port.DataSource)
	closableAdapters := make([]io.Closer, 0)
	pm, err := plugin_manager.NewPluginManager(sysDB, rootDir, config.PluginManagement.Repositories, config.PluginManagement.InstallDirectory, dataSourceRegistry, &closableAdapters)
	if err != nil {
		slog.Error("初始化 PluginManager 失败", "error", err)
		os.Exit(1)
	}
	slog.Info("服务层: PluginManager 初始化完成")

	rateLimiter := aegmiddleware.NewBusinessRateLimiter(adminConfigService, 10, 30)
	slog.Info("服务层: BusinessRateLimiter 初始化完成")

	pm.RefreshRepositories()
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			pm.RefreshRepositories()
		}
	}()
	slog.Info("后台任务: 插件仓库定期刷新已启动。")

	var setupToken string
	var setupTokenDeadline time.Time
	if service.UserCount(sysDB) == 0 {
		setupToken = genToken()
		setupTokenDeadline = time.Now().Add(30 * time.Minute)
		slog.Warn("系统中无管理员，安装令牌已生成 (30分钟内有效)", "setup_token", setupToken)
	}

	httpRouter := router.New(
		router.Dependencies{
			Registry:           dataSourceRegistry,
			AdminConfigService: adminConfigService,
			PluginManager:      pm,
			RateLimiter:        rateLimiter,
			AuthDB:             sysDB,
			SetupToken:         setupToken,
			SetupTokenDeadline: setupTokenDeadline,
		},
	)
	slog.Info("传输层: HTTP 路由器创建完成。")

	addr := fmt.Sprintf(":%d", config.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: httpRouter,
	}

	go func() {
		slog.Info("ArchiveAegis 内核启动成功，开始监听HTTP请求...", "address", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP服务启动失败", "error", err)
			os.Exit(1)
		}
	}()

	// 按需启用 pprof 并注册 prometheus metrics
	if enabledFeatures["io.archiveaegis.system.observability"] {
		aegobserve.EnablePprof("0.0.0.0:6060")
	}
	aegobserve.Register()
	slog.Info("监控: metrics 已注册。")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("收到停机信号，准备优雅关闭...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("HTTP服务优雅关闭失败", "error", err)
		os.Exit(1)
	}

	slog.Info("HTTP服务已成功关闭。")
	slog.Info("程序即将退出。")
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
