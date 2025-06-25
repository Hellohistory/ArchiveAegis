// Package plugin_lifecycle 提供插件实例生命周期管理器的核心结构与构造函数
// 文件位置: internal/service/plugin_manager/plugin_lifecycle/manager.go
package plugin_lifecycle

import (
	"ArchiveAegis/internal/adapter/datasource/grpc_client"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"database/sql"
	"io"
	"os/exec"
	"sync"
)

// ManifestProvider 用于按插件 ID 获取插件清单信息的函数类型。
type ManifestProvider func(pluginID string) (*domain.PluginManifest, bool)

const (
	StatusRunning = "RUNNING" // 插件实例运行状态
	StatusStopped = "STOPPED" // 插件实例停止状态
	StatusError   = "ERROR"   // 插件实例错误状态
)

// runningInstance 表示运行中的插件实例的内部状态信息。
type runningInstance struct {
	cmd      *exec.Cmd                  // 插件进程命令对象
	bizName  string                     // 所属业务组名称
	executor port.Executor              // gRPC 执行器
	adapter  *grpc_client.ClientAdapter // gRPC 客户端适配器
}

// LifecycleManager 管理插件实例的生命周期，包括启动、停止、状态同步等操作。
type LifecycleManager struct {
	db                 *sql.DB                     // 插件实例元数据数据库
	installDir         string                      // 插件安装目录
	getManifest        ManifestProvider            // 插件清单信息提供者
	runningInstances   map[string]*runningInstance // 当前运行中的插件实例集合
	runningInstancesMu sync.RWMutex                // 运行实例集合读写锁
	executorRegistry   map[string]port.Executor    // 执行器注册表，按业务组映射
	closableAdapters   *[]io.Closer                // 可关闭资源集合
}

// NewLifecycleManager 构造并返回一个新的插件生命周期管理器实例。
func NewLifecycleManager(
	db *sql.DB,
	installDir string,
	manifestProvider ManifestProvider,
	executorRegistry map[string]port.Executor,
	closableAdapters *[]io.Closer,
) *LifecycleManager {
	return &LifecycleManager{
		db:               db,
		installDir:       installDir,
		getManifest:      manifestProvider,
		executorRegistry: executorRegistry,
		closableAdapters: closableAdapters,
		runningInstances: make(map[string]*runningInstance),
	}
}
