// file: cmd/gateway/main.go
package main

import (
	"ArchiveAegis/internal/aegmiddleware"
	"ArchiveAegis/internal/aegobserve"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"ArchiveAegis/internal/transport/http/router"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/viper"
	_ "modernc.org/sqlite"
)

// 版本升级，标志着插件管理器架构的集成
const version = "v1.0.0-alpha3"

// PluginManagementConfig 对应 config.yaml 中的 `plugin_management` 部分
type PluginManagementConfig struct {
	InstallDirectory string                     `mapstructure:"install_directory"`
	Repositories     []service.RepositoryConfig `mapstructure:"repositories"`
}

// ServerConfig 对应 config.yaml 中的 `server` 部分
type ServerConfig struct {
	Port     int    `mapstructure:"port"`
	LogLevel string `mapstructure:"log_level"`
}

// Config 是整个 config.yaml 的顶层结构体
type Config struct {
	Server           ServerConfig           `mapstructure:"server"`
	PluginManagement PluginManagementConfig `mapstructure:"plugin_management"`
}

func main() {
	log.Printf("ArchiveAegis Universal Kernel %s 正在启动...", version)

	// =========================================================================
	//  1. 初始化配置
	// =========================================================================
	if err := initViper(); err != nil {
		log.Fatalf("CRITICAL: 初始化配置失败: %v", err)
	}
	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("CRITICAL: 解析配置到结构体失败: %v", err)
	}
	log.Println("✅ 配置: config.yaml 加载并解析成功。")

	// =========================================================================
	//  2. 初始化数据库和核心服务
	// =========================================================================
	instanceDir := "instance"
	if _, err := os.Stat(instanceDir); os.IsNotExist(err) {
		_ = os.MkdirAll(instanceDir, 0755)
	}
	authDbPath := filepath.Join(instanceDir, "auth.db")
	sysDB, err := initAuthDB(authDbPath)
	if err != nil {
		log.Fatalf("CRITICAL: 初始化认证数据库失败: %v", err)
	}
	defer func() {
		log.Println("正在关闭系统数据库连接...")
		if err := sysDB.Close(); err != nil {
			log.Printf("ERROR: 关闭系统数据库时发生错误: %v", err)
		}
	}()

	if err := service.InitPlatformTables(sysDB); err != nil {
		log.Fatalf("CRITICAL: 初始化平台系统表失败: %v", err)
	}

	adminConfigService, err := service.NewAdminConfigServiceImpl(sysDB, 1000, 5*time.Minute)
	if err != nil {
		log.Fatalf("CRITICAL: 初始化 AdminConfigService 失败: %v", err)
	}
	log.Println("✅ 服务层: AdminConfigService 初始化完成")

	dataSourceRegistry := make(map[string]port.DataSource)
	closableAdapters := make([]io.Closer, 0)
	pluginManager, err := service.NewPluginManager(sysDB, config.PluginManagement.Repositories, config.PluginManagement.InstallDirectory, dataSourceRegistry, &closableAdapters)
	if err != nil {
		log.Fatalf("CRITICAL: 初始化 PluginManager 失败: %v", err)
	}
	log.Println("✅ 服务层: PluginManager 初始化完成")

	rateLimiter := aegmiddleware.NewBusinessRateLimiter(adminConfigService, 10, 30)
	log.Println("✅ 服务层: BusinessRateLimiter 初始化完成")

	// =========================================================================
	//  3. 启动后台任务
	// =========================================================================
	pluginManager.RefreshRepositories() // 启动时立即刷新一次仓库
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pluginManager.RefreshRepositories()
			}
		}
	}()
	log.Println("✅ 后台任务: 插件仓库定期刷新已启动。")

	// =========================================================================
	//  4. 初始化并启动 HTTP 服务
	// =========================================================================
	var setupToken string
	var setupTokenDeadline time.Time
	if service.UserCount(sysDB) == 0 {
		setupToken = genToken()
		setupTokenDeadline = time.Now().Add(30 * time.Minute)
		log.Printf("重要: [SETUP MODE] 系统中无管理员，安装令牌已生成 (30分钟内有效): %s", setupToken)
	}

	httpRouter := router.New(
		router.Dependencies{
			Registry:           dataSourceRegistry,
			AdminConfigService: adminConfigService,
			PluginManager:      pluginManager,
			RateLimiter:        rateLimiter,
			AuthDB:             sysDB,
			SetupToken:         setupToken,
			SetupTokenDeadline: setupTokenDeadline,
		},
	)
	log.Println("✅ 传输层: HTTP 路由器创建完成。")

	addr := fmt.Sprintf(":%d", config.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: httpRouter,
	}

	go func() {
		log.Printf("🚀 ArchiveAegis 内核启动成功，开始在 %s 上监听 HTTP 请求...", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("CRITICAL: HTTP服务启动失败: %v", err)
		}
	}()

	aegobserve.EnablePprof()
	aegobserve.Register()
	log.Println("✅ 监控: pprof, metrics 已启用。")

	// =========================================================================
	//  5. 优雅停机处理
	// =========================================================================
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("👋 收到停机信号，准备优雅关闭...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("CRITICAL: HTTP服务优雅关闭失败: %v", err)
	}

	log.Println("✅ HTTP服务已成功关闭。")
	log.Println("程序即将退出。")
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

// initViper 辅助函数，用于处理配置文件
func initViper() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Println("警告: 未找到 config.yaml。将创建默认配置文件 config.yaml。")
			// 更新默认配置文件以匹配新的结构
			defaultConfig := `
# ArchiveAegis 平台默认配置文件 (V3 - 插件仓库模式)
server:
  port: 10224
  log_level: "info"

# 插件管理配置
plugin_management:
  # 插件将被下载和安装到这个目录
  install_directory: "./instance/plugins"
  
  # 插件仓库列表
  repositories:
    - name: "本地测试仓库"
      # 指向我们之前创建的本地清单文件，注意 file:// 协议头
      url: "file://./configs/local_repository.json"
      enabled: true
      
    - name: "ArchiveAegis 官方仓库 (示例)"
      url: "https://plugins.archiveaegis.io/repository.json"
      enabled: false # 默认禁用，因为地址是虚构的
`
			configFilePath := "configs/config.yaml"
			if err := os.MkdirAll("configs", 0755); err != nil {
				return fmt.Errorf("创建configs目录失败: %w", err)
			}
			if err := os.WriteFile(configFilePath, []byte(defaultConfig), 0644); err != nil {
				return fmt.Errorf("创建默认配置文件失败: %w", err)
			}
			log.Printf("警告: 默认配置文件已在 '%s' 创建。请根据需要修改。", configFilePath)
			// 重新读取刚刚创建的配置文件
			return viper.ReadInConfig()
		} else {
			return fmt.Errorf("读取配置文件时发生错误: %w", err)
		}
	}
	return nil
}
