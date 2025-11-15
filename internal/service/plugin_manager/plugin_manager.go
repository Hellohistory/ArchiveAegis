// Package plugin_manager 提供插件管理器的实现，包括插件目录、安装和生命周期管理功能
// 文件位置: internal/service/plugin_manager/plugin_manager.go
package plugin_manager

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/downloader"
	"ArchiveAegis/internal/service/plugin_manager/plugin_lifecycle"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// PluginManager 管理插件的目录、安装和运行时状态
type PluginManager struct {
	*plugin_lifecycle.LifecycleManager

	db               *sql.DB
	rootDir          string
	installDir       string
	repositories     []RepositoryConfig
	catalog          map[string]domain.PluginManifest
	downloaders      []downloader.Downloader
	executorRegistry map[string]port.Executor
	closableAdapters *[]io.Closer

	// 并发控制锁
	catalogMu sync.RWMutex
}

var ErrInstanceNotRunning = plugin_lifecycle.ErrInstanceNotRunning

// RepositoryConfig 表示插件仓库的配置项
type RepositoryConfig struct {
	Name    string `mapstructure:"name"`    // 仓库名称
	URL     string `mapstructure:"url"`     // 仓库地址
	Enabled bool   `mapstructure:"enabled"` // 是否启用
}

// GetExecutor 根据业务组名称（biz_name）获取对应的插件执行器。
func (pm *PluginManager) GetExecutor(bizName string) (port.Executor, bool) {
	// 直接委托给 LifecycleManager 的同名方法，该方法是线程安全的。
	return pm.LifecycleManager.GetExecutor(bizName)
}

// Reload 重新加载指定插件实例。
func (pm *PluginManager) Reload(instanceID string) error {
	return pm.LifecycleManager.Reload(instanceID)
}

// NewPluginManager 创建并返回一个新的插件管理器实例
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

	// 初始化 PluginManager 自身的核心字段
	pm := &PluginManager{
		db:               db,
		rootDir:          rootDir,
		installDir:       installDir,
		repositories:     repos,
		catalog:          make(map[string]domain.PluginManifest),
		downloaders:      supportedDownloaders,
		executorRegistry: registry,
		closableAdapters: closers,
	}

	// 定义一个 ManifestProvider 函数，它将作为参数传递给 LifecycleManager。
	// 这个函数让 LifecycleManager 能够按需从 PluginManager 获取插件的清单信息。
	manifestProvider := func(pluginID string) (*domain.PluginManifest, bool) {
		pm.catalogMu.RLock()
		defer pm.catalogMu.RUnlock()
		manifest, ok := pm.catalog[pluginID]
		// 返回清单的指针，即使未找到，也返回一个空指针和 false
		if !ok {
			return nil, false
		}
		return &manifest, true
	}

	// 初始化并嵌入 LifecycleManager
	lifecycleMgr := plugin_lifecycle.NewLifecycleManager(
		db,
		installDir,
		manifestProvider, // 传递上面定义的函数
		registry,
		closers,
	)

	// 将初始化好的 LifecycleManager 赋值给嵌入的字段
	pm.LifecycleManager = lifecycleMgr
	lifecycleMgr.StartHealthChecks(15 * time.Second)

	return pm, nil
}
