// file: cmd/gateway/main.go
package main

import (
	"ArchiveAegis/internal/adapter/datasource/grpc_client"
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

// 版本升级，标志着动态插件系统的实现
const version = "v1.0.0-alpha3"

type Config struct {
	Server  ServerConfig   `mapstructure:"server"`
	Plugins []PluginConfig `mapstructure:"plugins"`
}

type ServerConfig struct {
	Port     int    `mapstructure:"port"`
	LogLevel string `mapstructure:"log_level"`
}

type PluginConfig struct {
	Address string `mapstructure:"address"`
	Enabled bool   `mapstructure:"enabled"`
}

func main() {
	log.Printf("ArchiveAegis Universal Kernel %s 正在启动...", version)

	if err := initViper(); err != nil {
		log.Fatalf("CRITICAL: 初始化配置失败: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("CRITICAL: 解析配置到结构体失败: %v", err)
	}
	log.Println("✅ 配置: config.yaml 加载并解析成功。")

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

	adminConfigService, err := service.NewAdminConfigServiceImpl(sysDB, 1000, 5*time.Minute)
	if err != nil {
		log.Fatalf("CRITICAL: 初始化 AdminConfigService 失败: %v", err)
	}
	log.Println("✅ 服务层: AdminConfigService 初始化完成")

	if err := service.InitPlatformTables(sysDB); err != nil {
		log.Fatalf("CRITICAL: 初始化平台系统表失败: %v", err)
	}

	// =========================================================================
	//  数据源初始化: 全新的动态插件发现与注册逻辑
	// =========================================================================
	dataSourceRegistry := make(map[string]port.DataSource)
	closableAdapters := make([]io.Closer, 0)
	log.Println("⚙️ 注册中心: 开始根据 config.yaml 进行动态插件发现...")

	for _, pluginCfg := range config.Plugins {
		if !pluginCfg.Enabled {
			log.Printf("⚪️ 插件地址 '%s' 在配置中被禁用，已跳过。", pluginCfg.Address)
			continue
		}

		// 连接到插件
		adapter, err := grpc_client.New(pluginCfg.Address)
		if err != nil {
			log.Printf("⚠️  无法连接到插件 '%s': %v，已跳过。", pluginCfg.Address, err)
			continue
		}

		// 调用 GetPluginInfo 获取插件的自我描述信息
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, err := adapter.GetPluginInfo(ctx)
		cancel() // 及时释放上下文资源

		if err != nil {
			log.Printf("⚠️  从插件 '%s' 获取信息失败: %v，已跳过。", pluginCfg.Address, err)
			_ = adapter.Close() // 获取信息失败，关闭连接
			continue
		}

		log.Printf("🤝 已成功从 '%s' 获取插件信息: [名称: %s, 版本: %s]", pluginCfg.Address, info.Name, info.Version)

		// 根据插件信息，将其注册到网关的业务组中
		if len(info.SupportedBizNames) == 0 {
			log.Printf("⚠️  插件 '%s' 未声明任何支持的业务组 (supported_biz_names)，已跳过。", info.Name)
			_ = adapter.Close()
			continue
		}

		isRegistered := false
		for _, bizName := range info.SupportedBizNames {
			if _, exists := dataSourceRegistry[bizName]; exists {
				// 防止不同的插件声称处理同一个业务组
				log.Printf("⚠️  业务组 '%s' 已被其他插件注册，插件 '%s' 的此次声明被忽略。", bizName, info.Name)
				continue
			}
			dataSourceRegistry[bizName] = adapter
			isRegistered = true
			log.Printf("✅ 业务组 '%s' 已成功动态注册，由插件 '%s' (地址: %s) 提供服务。", bizName, info.Name, pluginCfg.Address)
		}

		if isRegistered {
			closableAdapters = append(closableAdapters, adapter) // 只有成功注册了至少一个业务组的适配器才需要被关闭
		} else {
			_ = adapter.Close() // 没有注册任何业务，关闭连接
		}
	}
	log.Println("✅ 注册中心: 动态插件发现与注册完成。")

	// 在停机时关闭所有可关闭的适配器连接
	defer func() {
		log.Println("正在关闭所有gRPC插件适配器连接...")
		for _, closer := range closableAdapters {
			if err := closer.Close(); err != nil {
				log.Printf("ERROR: 关闭适配器连接时发生错误: %v", err)
			}
		}
	}()

	// =========================================================================
	//  初始化传输层 (这部分及之后保持不变)
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

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("👋 收到停机信号，准备优雅关闭...")

	// 创建一个有超时的上下文
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

			defaultConfig := `
# ArchiveAegis 平台默认配置文件 (V2 - 动态插件)
server:
  port: 10224
  log_level: "info"

# 需要连接的插件列表
# 网关将尝试连接所有已启用的插件，并动态注册它们所声明的业务。
plugins:
  - address: "localhost:50051"
    enabled: true # 设为 true 来启用这个插件

  # - address: "localhost:50052"
  #   enabled: false
`
			configFilePath := "configs/config.yaml"
			if err := os.MkdirAll("configs", 0755); err != nil {
				return fmt.Errorf("创建configs目录失败: %w", err)
			}
			if err := os.WriteFile(configFilePath, []byte(defaultConfig), 0644); err != nil {
				return fmt.Errorf("创建默认配置文件失败: %w", err)
			}
			// 修改为警告而非致命错误，以便程序可以继续使用默认值运行
			log.Printf("警告: 默认配置文件已在 '%s' 创建。请根据需要修改。", configFilePath)
			// 重新读取刚刚创建的配置文件
			return viper.ReadInConfig()
		} else {
			return fmt.Errorf("读取配置文件时发生错误: %w", err)
		}
	}
	return nil
}
