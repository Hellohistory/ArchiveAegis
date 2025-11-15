// Package plugin_lifecycle 提供插件实例生命周期管理器的核心结构与构造函数
// file: internal/service/plugin_manager/plugin_lifecycle/manager.go
package plugin_lifecycle

import (
	"ArchiveAegis/internal/adapter/datasource/grpc_client"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"database/sql"
	"io"
	"os/exec"
	"sync"
	"time"
)

// ManifestProvider 用于按插件 ID 获取插件清单信息的函数类型。
type ManifestProvider func(pluginID string) (*domain.PluginManifest, bool)

const (
	StatusRunning    = "RUNNING"    // 插件实例运行状态
	StatusStopped    = "STOPPED"    // 插件实例停止状态
	StatusError      = "ERROR"      // 插件实例错误状态
	StatusRecovering = "RECOVERING" // 插件实例恢复中状态

	HealthStatusHealthy     = "HEALTHY"
	HealthStatusDegraded    = "DEGRADED"
	HealthStatusUnreachable = "UNREACHABLE"
	HealthStatusRecovering  = "RECOVERING"
)

// runningInstance 表示插件实例的运行时状态信息。
type runningInstance struct {
	cmd      *exec.Cmd                  // 插件进程命令对象
	bizName  string                     // 所属业务组名称
	executor port.Executor              // gRPC 执行器
	adapter  *grpc_client.ClientAdapter // gRPC 客户端适配器
	pid      int                        // 插件进程 PID

	protocolVersion      string    // 协议版本声明
	lastHeartbeat        time.Time // 最近一次健康检查成功时间
	failureCount         int       // 连续失败次数
	circuitOpenUntil     time.Time // 熔断截止时间
	autoRecoveryAttempts int       // 自动恢复尝试次数
	healthStatus         string    // 当前健康状态
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

// GetExecutor 根据业务组名称安全地获取对应的插件执行器。
func (lm *LifecycleManager) GetExecutor(bizName string) (port.Executor, bool) {
	lm.runningInstancesMu.RLock()
	defer lm.runningInstancesMu.RUnlock()
	executor, ok := lm.executorRegistry[bizName]
	return executor, ok
}
