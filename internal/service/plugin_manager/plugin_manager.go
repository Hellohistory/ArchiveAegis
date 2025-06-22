// Package plugin_manager file: internal/service/plugin_manager/plugin_manager.go
package plugin_manager

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/downloader"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// PluginManager 负责管理插件的目录、安装和生命周期。
type PluginManager struct {
	db               *sql.DB
	rootDir          string
	installDir       string
	repositories     []RepositoryConfig
	catalog          map[string]domain.PluginManifest
	downloaders      []downloader.Downloader
	runningPlugins   map[string]*exec.Cmd
	executorRegistry map[string]port.Executor // <--- 重大变更
	closableAdapters *[]io.Closer
	bizToInstanceID  map[string]string

	// Mutexes
	catalogMu        sync.RWMutex
	runningPluginsMu sync.Mutex
	registryMu       sync.RWMutex
}

// RepositoryConfig 是在网关主配置中定义的仓库信息
type RepositoryConfig struct {
	Name    string `mapstructure:"name"`
	URL     string `mapstructure:"url"`
	Enabled bool   `mapstructure:"enabled"`
}

// NewPluginManager 创建一个新的插件管理器实例
func NewPluginManager(db *sql.DB, rootDir string, repos []RepositoryConfig, installDir string, registry map[string]port.Executor, closers *[]io.Closer) (*PluginManager, error) { // <--- 参数类型变更
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

	return &PluginManager{
		db:               db,
		rootDir:          rootDir,
		installDir:       installDir,
		repositories:     repos,
		catalog:          make(map[string]domain.PluginManifest),
		downloaders:      supportedDownloaders,
		runningPlugins:   make(map[string]*exec.Cmd),
		executorRegistry: registry, // <--- 字段赋值变更
		closableAdapters: closers,
		bizToInstanceID:  make(map[string]string),
	}, nil
}
