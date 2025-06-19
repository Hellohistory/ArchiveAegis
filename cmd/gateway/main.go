// file: cmd/gateway/main.go
package main

import (
	"ArchiveAegis/internal/adapter/datasource/grpc_client"
	"ArchiveAegis/internal/adapter/datasource/sqlite"
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

	"ArchiveAegis/internal/aegobserve"

	"github.com/spf13/viper"
	_ "modernc.org/sqlite"
)

// version 定义当前程序的版本号
const version = "v1.0.0-alpha2" // 版本升级，标志着插件系统集成

// Config 结构体保持不变
type Config struct {
	Server      ServerConfig       `mapstructure:"server"`
	DataSources []DataSourceConfig `mapstructure:"datasources"`
}

type ServerConfig struct {
	Port     int    `mapstructure:"port"`
	LogLevel string `mapstructure:"log_level"`
}

type DataSourceConfig struct {
	Name    string                 `mapstructure:"name"`
	Type    string                 `mapstructure:"type"`
	Enabled bool                   `mapstructure:"enabled"`
	Params  map[string]interface{} `mapstructure:"params"`
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

	if err := service.InitUserTable(sysDB); err != nil {
		log.Fatalf("CRITICAL: 初始化用户表失败: %v", err)
	}
	log.Println("✅ 服务层: AuthService (用户表) 初始化完成")

	dataSourceRegistry := make(map[string]port.DataSource)

	closableAdapters := make([]io.Closer, 0)
	log.Println("⚙️ 注册中心: 开始根据 config.yaml 初始化数据源...")

	for _, dsConfig := range config.DataSources {
		if !dsConfig.Enabled {
			log.Printf("⚪️ 数据源 '%s' 在配置中被禁用，已跳过。", dsConfig.Name)
			continue
		}

		var dsAdapter port.DataSource
		var initErr error

		switch dsConfig.Type {
		case "sqlite_builtin":
			adapter := sqlite.NewManager(adminConfigService)
			if err := adapter.InitForBiz(context.Background(), instanceDir, dsConfig.Name); err != nil {
				initErr = fmt.Errorf("为 '%s' 初始化 'sqlite_builtin' 失败: %w", dsConfig.Name, err)
			}
			dsAdapter = adapter

		case "sqlite_plugin":
			address, ok := dsConfig.Params["address"].(string)
			if !ok {
				initErr = fmt.Errorf("gRPC插件 '%s' 的配置缺少 'address' 字符串参数", dsConfig.Name)
			} else {
				var adapter *grpc_client.ClientAdapter
				adapter, initErr = grpc_client.New(address, dsConfig.Type)
				if initErr == nil {
					dsAdapter = adapter
					closableAdapters = append(closableAdapters, adapter) // 添加到可关闭列表
				}
			}

		default:
			initErr = fmt.Errorf("未知的数据源类型 '%s' (用于 '%s')", dsConfig.Type, dsConfig.Name)
		}

		if initErr != nil {
			log.Printf("⚠️  初始化数据源 '%s' 失败: %v，已跳过。", dsConfig.Name, initErr)
			continue
		}

		dataSourceRegistry[dsConfig.Name] = dsAdapter
		log.Printf("✅ 数据源 '%s' (类型: %s) 成功注册。", dsConfig.Name, dsConfig.Type)
	}
	log.Println("✅ 注册中心: 所有已启用的数据源均已初始化并填充完成。")

	// ✅ FINAL-MOD: 在停机时关闭所有可关闭的适配器连接
	defer func() {
		log.Println("正在关闭所有gRPC插件适配器连接...")
		for _, closer := range closableAdapters {
			if err := closer.Close(); err != nil {
				log.Printf("ERROR: 关闭适配器连接时发生错误: %v", err)
			}
		}
	}()

	// =========================================================================
	//  3. 初始化传输层
	// =========================================================================
	var setupToken string
	var setupTokenDeadline time.Time
	if service.UserCount(sysDB) == 0 {
		setupToken = genToken()
		// ✅ FINAL-MOD: 完善安装流程，传递过期时间
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
		// 如果错误是“文件未找到”，则创建默认配置文件
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Println("警告: 未找到 config.yaml。将创建默认配置文件 config.yaml。")
			defaultConfig := `
# ArchiveAegis 平台默认配置文件
server:
  port: 10224
  log_level: "info"

datasources:
  - name: "local_data"
    type: "sqlite_builtin"
    enabled: true
    params:
      directory: "local_data" # 将会扫描 instance/local_data/ 目录下的 .db 文件

  - name: "my_first_plugin"
    type: "sqlite_plugin"
    enabled: false # 默认禁用，请在启动插件后设为 true
    params:
      address: "localhost:50051"
`
			configFilePath := "configs/config.yaml"
			if err := os.MkdirAll("configs", 0755); err != nil {
				return fmt.Errorf("创建configs目录失败: %w", err)
			}
			if err := os.WriteFile(configFilePath, []byte(defaultConfig), 0644); err != nil {
				return fmt.Errorf("创建默认配置文件失败: %w", err)
			}
			log.Fatalf("CRITICAL: 默认配置文件已在 '%s' 创建。请根据您的需求修改它，并将其重命名为 'config.yaml' 后，再重新启动程序。", configFilePath)
		} else {
			return fmt.Errorf("读取配置文件时发生错误: %w", err)
		}
	}
	return nil
}
