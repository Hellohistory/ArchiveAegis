// Package main — ArchiveAegis 轻量多业务 SQLite 检索服务入口
package main

import (
	"ArchiveAegis/aegapi"
	"ArchiveAegis/aegauth"
	"ArchiveAegis/aegconf"
	"ArchiveAegis/aegdb"
	"ArchiveAegis/aegdebug"
	"ArchiveAegis/aegmetric"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// version 定义当前程序的版本号
const version = "v0.2.6"

func genToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Printf("警告: 生成随机 token 时 rand.Read 失败: %v。使用伪随机后备。", err)
		for i := range b {
			b[i] = byte(time.Now().UnixNano() + int64(i))
		}
	}
	return hex.EncodeToString(b)
}

func main() {

	cfg := aegconf.Load()
	log.Printf("ArchiveAegis %s 正在启动... (配置端口: %d)", version, cfg.Port)

	instanceDir := "instance"
	if _, err := os.Stat(instanceDir); os.IsNotExist(err) {
		if errCreate := os.MkdirAll(instanceDir, 0755); errCreate != nil {
			log.Fatalf("CRITICAL: 创建 instance 目录 '%s' 失败: %v", instanceDir, errCreate)
		}
		log.Printf("信息: 已创建 instance 目录: %s", instanceDir)
	}

	authDbPath := filepath.Join(instanceDir, "auth.db")
	log.Printf("信息: 初始化认证数据库: %s", authDbPath)
	authDSN := fmt.Sprintf("file:%s?_busy_timeout=10000&_journal_mode=WAL&_foreign_keys=ON&_synchronous=NORMAL", authDbPath)
	sysDB, err := sql.Open("sqlite", authDSN)
	if err != nil {
		log.Fatalf("CRITICAL: 打开/创建认证数据库 '%s' 失败: %v", authDbPath, err)
	}
	defer func() {
		if errClose := sysDB.Close(); errClose != nil {
			log.Printf("警告: 关闭认证数据库时发生错误: %v", errClose)
		}
	}()

	if errPing := sysDB.Ping(); errPing != nil {
		log.Fatalf("CRITICAL: 连接认证数据库 '%s' (Ping) 失败: %v", authDbPath, errPing)
	}

	if err := aegauth.InitUserTable(sysDB); err != nil {
		log.Fatalf("CRITICAL: 初始化用户表失败 (认证数据库 '%s'): %v", authDbPath, err)
	}
	log.Println("信息: 认证系统数据库及用户表初始化完成。")

	maxCacheEntries := 1000
	defaultCacheTTL := 5 * time.Minute
	adminConfigService, err := aegdb.NewAdminConfigServiceImpl(sysDB, maxCacheEntries, defaultCacheTTL)
	if err != nil {
		log.Fatalf("CRITICAL: 初始化 AdminConfigService 失败: %v", err)
	}
	log.Println("信息: AdminConfigService (查询配置服务) 初始化完成。")

	manager := aegdb.NewManager(adminConfigService)
	log.Println("信息: 正在初始化业务数据库管理器...")

	if errInit := manager.Init(context.Background(), instanceDir); errInit != nil {
		log.Printf("警告: 初始化业务数据库管理器过程中遇到指示性错误: %v。服务仍将继续运行。", errInit)
	} else {
		log.Println("信息: 业务数据库管理器首次扫描完成。")
	}

	if errWatcher := manager.StartWatcher(instanceDir); errWatcher != nil {
		log.Printf("警告: 启动业务数据库文件监视器失败: %v。数据库热加载功能可能受限或不可用。", errWatcher)
	} else {
		log.Println("信息: 业务数据库文件监视器已启动。")
	}

	userCount := aegauth.UserCount(sysDB)
	if userCount == 0 {
		setupTokenValue := genToken() // Renamed to avoid conflict with aegapi.setupToken if it were still global here
		setupTokenDead := time.Now().Add(30 * time.Minute)
		aegapi.SetSetupToken(setupTokenValue, setupTokenDead) // Pass to aegapi package
		log.Printf("重要: [SETUP MODE] 系统中尚无管理员账户。请在30分钟内使用以下令牌通过 /setup 接口创建管理员:")
		log.Printf("          令牌: %s", setupTokenValue)
		log.Printf("          (请保管好此令牌，仅显示一次。如果浏览器访问，可能是 /setup?token=%s )", setupTokenValue)
	} else if userCount < 0 {
		log.Printf("警告: 检查用户数时出现问题 (UserCount 返回 %d)。请检查认证数据库 '%s'。", userCount, authDbPath)
	}

	aegdebug.EnablePprof()
	aegmetric.Register()
	log.Println("信息: 调试工具 (pprof, metrics) 已启用。")

	baseHandler := aegapi.NewRouter(manager, sysDB, adminConfigService)

	addr := ":" + strconv.Itoa(cfg.Port)
	server := &http.Server{
		Addr:              addr,
		Handler:           baseHandler, // 直接使用 baseHandler
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("信息: ArchiveAegis 服务已准备就绪，开始在 %s 上监听 HTTP 请求...", addr)
	if errServe := server.ListenAndServe(); !errors.Is(errServe, http.ErrServerClosed) {
		log.Fatalf("CRITICAL: HTTP 服务启动或运行时发生致命错误: %v", errServe)
	} else {
		log.Println("信息: HTTP 服务已平滑关闭。")
	}

	log.Println("信息: ArchiveAegis 服务正在关闭。")
}
