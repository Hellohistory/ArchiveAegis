// Package plugin_manager 提供插件的目录管理、安装及生命周期控制功能
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
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// PluginManager 管理插件的元数据、安装流程及运行时生命周期。
type PluginManager struct {
	db           *sql.DB                 // 插件元数据存储数据库
	rootDir      string                  // 插件系统根目录
	installDir   string                  // 插件安装目录
	repositories []RepositoryConfig      // 插件源配置
	downloaders  []downloader.Downloader // 支持的下载器列表

	catalog   map[string]domain.PluginManifest // 插件清单缓存
	catalogMu sync.RWMutex                     // 插件清单访问锁

	LifecycleMgr *plugin_lifecycle.LifecycleManager // 插件生命周期管理器
}

// RepositoryConfig 定义插件源的配置项。
type RepositoryConfig struct {
	Name    string `mapstructure:"name"`    // 仓库名称
	URL     string `mapstructure:"url"`     // 仓库地址
	Enabled bool   `mapstructure:"enabled"` // 是否启用
}

// NewPluginManager 构造并初始化插件管理器实例。
func NewPluginManager(
	db *sql.DB,
	rootDir string,
	repos []RepositoryConfig,
	installDir string,
	registry map[string]port.Executor,
	closers *[]io.Closer,
) (*PluginManager, error) {
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
	}

	manifestProvider := func(pluginID string) (*domain.PluginManifest, bool) {
		pm.catalogMu.RLock()
		defer pm.catalogMu.RUnlock()
		manifest, ok := pm.catalog[pluginID]
		if !ok {
			return nil, false
		}
		return &manifest, true
	}

	pm.LifecycleMgr = plugin_lifecycle.NewLifecycleManager(
		db,
		installDir,
		manifestProvider,
		registry,
		closers,
	)

	if err := pm.LifecycleMgr.ReconcileOrphanedPlugins(); err != nil {
		log.Printf("[PluginManager] WARNING: 启动时清理孤儿插件进程失败: %v", err)
	}

	return pm, nil
}
