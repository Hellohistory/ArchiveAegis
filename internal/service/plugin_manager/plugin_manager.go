// file: internal/service/plugin_manager/plugin_manager.go

package plugin_manager

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/downloader"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// 统一的运行时实例结构体 ---
// runningInstance 封装了一个正在运行的插件实例的所有运行时状态。
type runningInstance struct {
	cmd      *exec.Cmd     // 插件的系统进程
	executor port.Executor // 与插件通信的gRPC客户端
	bizName  string        // 该实例绑定的业务名称
}

// PluginManager 负责管理插件的目录、安装和生命周期。
type PluginManager struct {
	db                 *sql.DB
	rootDir            string
	installDir         string
	repositories       []RepositoryConfig
	catalog            map[string]domain.PluginManifest
	downloaders        []downloader.Downloader
	runningInstances   map[string]*runningInstance // Key: instanceID
	runningInstancesMu sync.RWMutex

	executorRegistry map[string]port.Executor
	closableAdapters *[]io.Closer

	// catalog 的访问也需要并发安全
	catalogMu sync.RWMutex
}

// RepositoryConfig 是在网关主配置中定义的仓库信息
type RepositoryConfig struct {
	Name    string `mapstructure:"name"`
	URL     string `mapstructure:"url"`
	Enabled bool   `mapstructure:"enabled"`
}

// NewPluginManager 创建一个新的插件管理器实例
func NewPluginManager(db *sql.DB, rootDir string, repos []RepositoryConfig, installDir string, registry map[string]port.Executor, closers *[]io.Closer) (*PluginManager, error) {
	if db == nil {
		return nil, errors.New("PluginManager 需要一个有效的数据库连接")
	}
	if installDir == "" {
		return nil, fmt.Errorf("插件安装目录(installDir)不能为空")
	}
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return nil, fmt.Errorf("创建插件安装目录 '%s' 失败: %w", installDir, err)
	}

	supportedDownloaders := []downloader.Downloader{
		&downloader.HTTPDownloader{
			Client: &http.Client{Timeout: 60 * time.Second},
		},
		&downloader.FileDownloader{},
	}

	pm := &PluginManager{
		db:           db,
		rootDir:      rootDir,
		installDir:   installDir,
		repositories: repos,
		catalog:      make(map[string]domain.PluginManifest),
		downloaders:  supportedDownloaders,
		// --- 修改：初始化新的字段 ---
		runningInstances: make(map[string]*runningInstance),
		executorRegistry: registry,
		closableAdapters: closers,
	}

	// 在启动时执行孤儿进程清理
	if err := pm.ReconcileOrphanedPlugins(); err != nil {
		log.Printf("⚠️ [PluginManager] 启动时清理孤儿插件进程失败: %v", err)
	}

	return pm, nil
}
