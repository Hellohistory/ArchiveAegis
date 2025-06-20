// Package sqlite — 多库 / 多业务组 SQLite 管理器 (重构版)
// internal/adapter/datasource/sqlite/manager.go
package sqlite

import (
	"ArchiveAegis/internal/core/port"
	"database/sql"
	"log"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// 断言 *Manager 实现 port.DataSource 接口，编译期校验
var _ port.DataSource = (*Manager)(nil)

const (
	debounceDuration = 2 * time.Second
)

// Manager 是 SQLite 数据源适配器的核心结构体。
// 它管理按业务组划分的多个 SQLite 数据库连接，并处理查询、写入、Schema发现和文件热加载。
type Manager struct {
	mu sync.RWMutex

	// root 是实例目录的根路径, e.g., "instance"
	root string

	// group 存储所有已加载的数据库连接，按 [bizName][libName] 组织
	group map[string]map[string]*sql.DB

	// dbSchemaCache 缓存每个数据库连接的物理 Schema 信息
	dbSchemaCache map[*sql.DB]*dbPhysicalSchemaInfo

	// schema 缓存每个业务组下所有库的物理表及列的并集
	schema map[string]map[string][]string

	// eventTimers 用于文件系统事件的防抖处理
	eventTimers   map[string]*time.Timer
	eventTimersMu sync.Mutex

	// configService 用于在查询和写入时获取权限配置
	configService port.QueryAdminConfigService
}

// NewManager 创建一个新的 Manager 实例。
func NewManager(cfgService port.QueryAdminConfigService) *Manager {
	if cfgService == nil {
		log.Fatal("[DBManager] 致命错误: QueryAdminConfigService 实例不能为 nil。")
	}
	return &Manager{
		group:         make(map[string]map[string]*sql.DB),
		dbSchemaCache: make(map[*sql.DB]*dbPhysicalSchemaInfo),
		schema:        make(map[string]map[string][]string),
		eventTimers:   make(map[string]*time.Timer),
		configService: cfgService,
	}
}

// Type 实现 port.DataSource.Type 接口，返回适配器类型。
func (m *Manager) Type() string {
	return "sqlite_builtin"
}

// Summary 返回一个映射，表示每个业务组 (bizName) 下有哪些库文件 (libName)。
func (m *Manager) Summary() map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	summaryMap := make(map[string][]string, len(m.group))
	for bizName, libsInBiz := range m.group {
		if len(libsInBiz) > 0 {
			libNames := make([]string, 0, len(libsInBiz))
			for libName := range libsInBiz {
				libNames = append(libNames, libName)
			}
			sort.Strings(libNames)
			summaryMap[bizName] = libNames
		}
	}
	return summaryMap
}
