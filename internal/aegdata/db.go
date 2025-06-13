// Package aegdata — 多库 / 多业务组 SQLite 管理器 (重构版)
package aegdata

import (
	"ArchiveAegis/internal/aeglogic"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort" // 用于对表名、列名等进行排序，以保证输出顺序的稳定性
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sync/errgroup"
	_ "modernc.org/sqlite" // SQLite 驱动
)

/*
================================================================================

	aegdb 包级错误定义

================================================================================
*/

// 包级错误变量，用于指示特定的操作失败原因。
var (
	// ErrPermissionDenied 表示操作因权限不足而被拒绝。
	ErrPermissionDenied = errors.New("权限不足，操作被拒绝")

	// ErrBizNotFound 表示请求的业务组在系统中不存在或未被加载。
	ErrBizNotFound = errors.New("指定的业务组未找到")

	// ErrTableNotFoundInBiz 表示在给定的业务组配置中，未找到用户请求的表。
	// 这与物理表是否存在于数据库文件中是不同的概念；这里指的是管理员配置层面。
	ErrTableNotFoundInBiz = errors.New("在当前业务组的配置中未找到指定的表")
)

/*
================================================================================

	aegdb 内部常量定义

================================================================================
*/
const (
	// innerPrefix 用于标识 ArchiveAegis 内部自动管理的表或对象，
	innerPrefix = "_archiveaegis_internal_" // 例如：_archiveaegis_internal_some_table

	// schemaCacheFilename 是用于存储每个业务组物理表结构并集缓存的文件名。
	schemaCacheFilename = "schema_cache.json"

	// debounceDuration 是文件系统事件处理的防抖延迟。
	debounceDuration = 2 * time.Second
)

/*
================================================================================
  aegdb 内部核心结构体定义
================================================================================
*/

// dbPhysicalSchemaInfo 存储从单个数据库文件实际扫描到的物理结构信息。
// 这与管理员配置的“逻辑”查询schema (通过QueryAdminConfigService获取) 是分开的。
type dbPhysicalSchemaInfo struct {
	detectedDefaultTable string              // 自动检测到的默认表名 (通常是按字母顺序的第一个用户表)
	allTablesAndColumns  map[string][]string // 物理表名 -> 该表所有物理列名的列表
}

// Manager 是 aegdb 包的核心，负责管理多个业务组及其下的 SQLite 数据库文件。
type Manager struct {
	mu            sync.RWMutex                      // 保护 Manager 内部状态的读写锁
	root          string                            // instance 目录的根路径, 例如 "instance"
	group         map[string]map[string]*sql.DB     // bizName -> libName -> *sql.DB (数据库连接池)
	dbSchemaCache map[*sql.DB]*dbPhysicalSchemaInfo // *sql.DB -> 该库的物理Schema信息缓存

	// configService 是外部依赖，用于获取管理员定义的查询配置。
	configService aeglogic.QueryAdminConfigService

	// schema 用于存储每个业务组下所有库的物理表结构“并集”的缓存。
	// 它的 key 是业务组名称(bizName)，value 是一个 map，这个 map 的 key 是表名(tableName)，
	// value 是一个 string 切片，表示该表在业务组所有库中出现过的所有列名的并集。
	schema map[string]map[string][]string // bizName -> tableName -> union of physical columnNames

	// 文件监控相关，用于热加载/卸载数据库文件。
	eventTimers   map[string]*time.Timer // path -> timer
	eventTimersMu sync.Mutex             // 保护 eventTimers
}

/*
================================================================================
  Manager 构造与初始化
================================================================================
*/

// NewManager 创建并返回一个新的 Manager 实例。
// 它需要一个 QueryAdminConfigService 的实例来获取查询配置。
func NewManager(cfgService aeglogic.QueryAdminConfigService) *Manager {
	if cfgService == nil {
		// 这是一个严重错误，Manager 无法在没有配置服务的情况下正确运行其核心查询逻辑。
		log.Fatal("[DBManager] 致命错误: QueryAdminConfigService 实例不能为 nil。Manager 初始化失败。")
	}
	return &Manager{
		group:         make(map[string]map[string]*sql.DB),
		dbSchemaCache: make(map[*sql.DB]*dbPhysicalSchemaInfo),
		schema:        make(map[string]map[string][]string),
		eventTimers:   make(map[string]*time.Timer),
		configService: cfgService, // 存储配置服务实例
	}
}

// Init 初始化 Manager，扫描指定根目录下的所有业务数据库。
// rootDir 是 "instance" 目录。
func (m *Manager) Init(ctx context.Context, rootDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.root = filepath.Clean(rootDir)

	log.Printf("[DBManager] 开始初始化，扫描业务数据库目录: %s", m.root)
	// 模式匹配 instance/<bizName>/<libName>.db
	files, err := filepath.Glob(filepath.Join(m.root, "*", "*.db"))
	if err != nil {
		// 如果 Glob 本身失败，通常是模式有问题或权限问题，记录错误但允许服务继续。
		log.Printf("错误: [DBManager] Init 时扫描数据库目录 '%s' (模式: %s) 失败: %v。管理器将以空状态运行。",
			m.root, filepath.Join(m.root, "*", "*.db"), err)
		return nil // 非致命，允许服务以空Manager启动
	}

	if len(files) == 0 {
		log.Printf("信息: [DBManager] Init 时在 '%s' 目录下未找到任何符合 '*.db' 的文件。", m.root)
	}

	var loadedCount int
	var errorMessages []string
	for _, f := range files {
		relPath, errRel := filepath.Rel(m.root, f)
		if errRel != nil {
			log.Printf("警告: [DBManager] Init 时无法获取文件 '%s' 相对于根目录 '%s' 的路径: %v，已跳过。", f, m.root, errRel)
			errorMessages = append(errorMessages, fmt.Sprintf("文件 '%s' (无法获取相对路径): %v", f, errRel))
			continue
		}
		if strings.Count(filepath.ToSlash(relPath), "/") != 1 {
			log.Printf("信息: [DBManager] Init 时文件 '%s' (相对路径 '%s') 不符合 'bizName/libName.db' 结构，已跳过。", f, relPath)
			continue
		}

		// 调用内部的 openDBInternal 来打开并加载物理schema
		if errOpen := m.openDBInternal(ctx, f); errOpen != nil {
			errMsg := fmt.Sprintf("文件 '%s': %v", f, errOpen)
			log.Printf("警告: [DBManager] Init 时打开数据库失败: %s", errMsg)
			errorMessages = append(errorMessages, errMsg)
		} else {
			loadedCount++
		}
	}

	log.Printf("[DBManager] 初始化扫描完成。成功加载 %d 个数据库。", loadedCount)
	if len(errorMessages) > 0 {
		log.Printf("警告: [DBManager] 初始化过程中有 %d 个数据库文件加载失败，详情: %s", len(errorMessages), strings.Join(errorMessages, "; "))
	}

	// 初始化或刷新 m.schema (所有业务组的物理表结构并集缓存)
	m.loadOrRefreshSchemaInternal()
	return nil
}

/*
================================================================================
  数据库文件物理结构加载 (核心是 loadDBPhysicalSchema)
================================================================================
*/

// loadDBPhysicalSchema 从给定的数据库连接中加载其实际的物理表和列信息。
// 它只关心数据库文件本身的结构，不关心管理员配置的查询逻辑。
func loadDBPhysicalSchema(ctx context.Context, db *sql.DB) (*dbPhysicalSchemaInfo, error) {
	// 自动检测一个 "默认表" (例如，按名称排序的第一个用户表)
	autoDetectedDefaultTable, errDetect := detectTable(db)
	if errDetect != nil && errDetect != sql.ErrNoRows {
		log.Printf("警告: [DBManager] loadDBPhysicalSchema: 自动检测默认表失败: %v。将继续加载其他schema信息。", errDetect)
		// 非致命，允许继续
	}
	if errDetect == sql.ErrNoRows {
		autoDetectedDefaultTable = "" // 表示此数据库中没有找到符合条件的用户表
		log.Printf("信息: [DBManager] loadDBPhysicalSchema: 数据库中未找到可作为默认的用户表。")
	}

	// 获取数据库中所有用户表的名称
	actualUserTables, errTables := getTablesSet(db)
	if errTables != nil {
		return nil, fmt.Errorf("loadDBPhysicalSchema: 获取物理表集合失败: %w", errTables)
	}

	// 获取每个用户表的物理列信息
	allTablesAndPhysColumns := make(map[string][]string)
	if len(actualUserTables) > 0 {
		for tblName := range actualUserTables {
			physColumns, errCols := listColumns(db, tblName)
			if errCols != nil {
				log.Printf("警告: [DBManager] loadDBPhysicalSchema: 表 '%s' 获取物理列信息失败: %v。此表将被视为空列列表。", tblName, errCols)
				allTablesAndPhysColumns[tblName] = []string{}
				continue
			}
			sort.Strings(physColumns) // 保证列顺序一致性
			allTablesAndPhysColumns[tblName] = physColumns
		}
	}

	physicalSchema := &dbPhysicalSchemaInfo{
		detectedDefaultTable: autoDetectedDefaultTable,
		allTablesAndColumns:  allTablesAndPhysColumns,
	}
	log.Printf("调试: [DBManager] loadDBPhysicalSchema: 加载完成。检测到默认表: '%s', 表总数: %d", autoDetectedDefaultTable, len(allTablesAndPhysColumns))
	return physicalSchema, nil
}

// getTablesSet 返回数据库中所有用户表的集合 (排除 sqlite_ 和 innerPrefix 开始的表)
func getTablesSet(db *sql.DB) (map[string]struct{}, error) {
	query := `
		SELECT name FROM sqlite_master
		WHERE type='table'
		  AND name NOT LIKE 'sqlite_%'      -- 排除 SQLite 系统表
		  AND name NOT LIKE ?               -- 排除应用内部表
	`
	rows, err := db.Query(query, innerPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("查询 sqlite_master 获取表集合失败: %w (SQL: %s)", err, query)
	}
	defer rows.Close()

	set := make(map[string]struct{})
	for rows.Next() {
		var tbl string
		if errScan := rows.Scan(&tbl); errScan != nil {
			log.Printf("警告: [DBManager] getTablesSet 时扫描表名失败: %v", errScan)
			continue
		}
		set[tbl] = struct{}{}
	}
	if errRows := rows.Err(); errRows != nil { // 检查迭代过程中的整体错误
		return set, fmt.Errorf("迭代表名结果时发生错误: %w", errRows)
	}
	return set, nil
}

// detectTable 尝试检测数据库中的一个 "默认" 用户表（例如按名称排序的第一个）。
// 如果没有用户表，返回 sql.ErrNoRows。
func detectTable(db *sql.DB) (string, error) {
	var name string
	query := `
		SELECT name FROM sqlite_master
		WHERE type='table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name NOT LIKE ?
		ORDER BY name ASC LIMIT 1  -- 按名称升序取第一个
	`
	err := db.QueryRow(query, innerPrefix+"%").Scan(&name)

	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows // 标准错误，表示未找到
	}
	if err != nil {
		return "", fmt.Errorf("查询 sqlite_master (detectTable) 失败: %w (SQL: %s)", err, query)
	}
	return name, nil
}

// listColumns 返回指定表的所有物理列名。
func listColumns(db *sql.DB, tableName string) ([]string, error) {
	// 使用 PRAGMA table_info 来获取列信息，表名需要被正确引用以防SQL注入。
	// fmt.Sprintf("%q", tableName) 会用双引号包裹表名，并转义内部的双引号。
	query := fmt.Sprintf(`PRAGMA table_info(%q)`, tableName)
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("PRAGMA table_info for table %q 失败: %w (SQL: %s)", tableName, err, query)
	}
	defer rows.Close()

	var cols []string
	// PRAGMA table_info 返回的列: cid, name, type, notnull, dflt_value, pk
	// 只关心 'name' (第二列，索引为1)。
	for rows.Next() {
		var (
			cid       int
			colName   string
			colType   string
			notnull   int
			dfltValue sql.NullString // 或者 *string
			pk        int
		)
		if errScan := rows.Scan(&cid, &colName, &colType, &notnull, &dfltValue, &pk); errScan != nil {
			log.Printf("警告: [DBManager] listColumns for table '%s' 时扫描列信息失败: %v", tableName, errScan)
			continue
		}
		cols = append(cols, colName)
	}
	if errRows := rows.Err(); errRows != nil { // 检查迭代过程中的整体错误
		return cols, fmt.Errorf("迭代表 '%s' 的列信息结果时发生错误: %w", tableName, errRows)
	}
	sort.Strings(cols) // 保证返回的列名顺序是固定的
	return cols, nil
}

/*
================================================================================
  数据库文件打开、关闭、热加载逻辑
================================================================================
*/

// openDBInternal 是打开单个数据库文件、加载其物理schema并更新Manager内部状态的私有方法。
func (m *Manager) openDBInternal(ctx context.Context, path string) error {
	rel, errRel := filepath.Rel(m.root, path)
	if errRel != nil {
		return fmt.Errorf("无法获取文件 '%s' 相对于根目录 '%s' 的路径: %w", path, m.root, errRel)
	}

	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2) // filepath.ToSlash 保证跨平台路径分隔符一致性
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("非法数据库路径结构 (应为 <bizName>/<libName>.db): '%s'", rel)
	}
	bizName, fileName := parts[0], parts[1]
	libName := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	if libName == "" {
		return fmt.Errorf("从文件名 '%s' 解析库名称失败 (biz: %s)", fileName, bizName)
	}

	// 构建 DSN (Data Source Name)
	// WAL模式 (Write-Ahead Logging) 提高并发性能和数据完整性。
	// _busy_timeout 设置当数据库锁定时，连接尝试等待的时间（毫秒）。
	// _foreign_keys=ON 启用外键约束。
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=ON", path)
	db, err := sql.Open("sqlite", dsn) // "sqlite" 是驱动名称，由 _ "modernc.org/sqlite" 注册
	if err != nil {
		return fmt.Errorf("sql.Open '%s' (DSN: %s) 失败: %w", path, dsn, err)
	}

	// sql.Open 不会立即建立连接或验证DSN，需要Ping来实际测试连接。
	if errPing := db.PingContext(ctx); errPing != nil {
		_ = db.Close() // Ping失败，尝试关闭已打开的句柄
		return fmt.Errorf("ping 数据库 '%s' 失败: %w", path, errPing)
	}

	// 加载数据库的物理 schema
	phySchema, errLoadPhysicalSchema := loadDBPhysicalSchema(ctx, db)
	if errLoadPhysicalSchema != nil {
		_ = db.Close() // schema加载失败，尝试关闭
		return fmt.Errorf("加载数据库 '%s' 的物理 schema 失败: %w", path, errLoadPhysicalSchema)
	}

	// 更新 Manager 状态 (在锁保护下)
	if m.group[bizName] == nil {
		m.group[bizName] = make(map[string]*sql.DB)
	}
	m.group[bizName][libName] = db
	m.dbSchemaCache[db] = phySchema // 存储物理schema信息

	log.Printf("信息: [DBManager] 成功打开并加载数据库: %s/%s (路径: %s)", bizName, libName, path)
	return nil
}

// openDB 是 openDBInternal 的公开包装器，带锁。
func (m *Manager) openDB(ctx context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.openDBInternal(ctx, path)
}

// closeDB 关闭指定路径的数据库连接，并清理相关缓存。
func (m *Manager) closeDB(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rel, errRel := filepath.Rel(m.root, path)
	if errRel != nil {
		log.Printf("警告: [DBManager] closeDB 时无法获取文件 '%s' 的相对路径: %v", path, errRel)
		return
	}
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
	if len(parts) != 2 {
		log.Printf("警告: [DBManager] closeDB 时文件 '%s' 路径结构不符，无法定位业务组和库名。", path)
		return
	}
	bizName, fileName := parts[0], parts[1]
	libName := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	if bizGroup, bizExists := m.group[bizName]; bizExists {
		if db, libExists := bizGroup[libName]; libExists {
			// 从缓存中删除物理 schema 信息
			if _, schemaCached := m.dbSchemaCache[db]; schemaCached {
				delete(m.dbSchemaCache, db)
			}
			// 关闭数据库连接
			if errClose := db.Close(); errClose != nil {
				log.Printf("警告: [DBManager] 关闭数据库 %s/%s (路径 '%s') 时发生错误: %v", bizName, libName, path, errClose)
			} else {
				log.Printf("信息: [DBManager] 成功关闭数据库: %s/%s (路径: %s)", bizName, libName, path)
			}
			// 从 group 中移除
			delete(bizGroup, libName)
			if len(bizGroup) == 0 { // 如果业务组下没有其他库了
				delete(m.group, bizName)
				// 当业务组被移除时，也应清理其在 m.schema 中的缓存
				delete(m.schema, bizName)
				log.Printf("信息: [DBManager] 业务组 '%s' 下已无数据库，已从Manager中清理。", bizName)
			}
		}
	}
}

// StartWatcher 启动文件系统监视器，用于热加载/卸载数据库。
func (m *Manager) StartWatcher(rootDir string) error {
	// rootDir 应该是 m.root，确保一致性
	if m.root == "" { // Manager可能尚未初始化root
		m.root = filepath.Clean(rootDir)
	}
	log.Printf("[DBManager] 尝试启动文件监视器于目录: %s", m.root)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("错误: [DBManager] 创建文件监视器失败: %v。数据库热加载/卸载功能将不可用。", err)
		return fmt.Errorf("创建 fsnotify watcher 失败: %w", err)
	}

	// 监视根目录，以检测新业务组目录的创建
	if err := watcher.Add(m.root); err != nil {
		log.Printf("错误: [DBManager] 添加根目录 '%s' 到监视器失败: %v。新业务组的热添加可能受影响。", m.root, err)
	} else {
		log.Printf("信息: [DBManager] 已成功添加根目录 '%s' 到监视器 (用于新业务组目录检测)。", m.root)
	}

	// 监视已存在的业务组目录，以检测其下 .db 文件的变化
	// (Init时已经扫描过一次，这里是为启动后的变化做准备)
	m.mu.RLock() // 读取 m.group 时需要锁
	for bizName := range m.group {
		bizPath := filepath.Join(m.root, bizName)
		if err := watcher.Add(bizPath); err != nil {
			log.Printf("警告: [DBManager] 添加现有业务目录 '%s' 到监视器失败: %v。", bizPath, err)
		}
	}
	m.mu.RUnlock()

	go func() {
		defer watcher.Close()
		log.Printf("信息: [DBManager] 文件监视 goroutine 已启动。")
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok { // Events 通道关闭
					log.Printf("警告: [DBManager] 文件监视器事件通道已关闭。goroutine 退出。")
					return
				}
				m.handleFsEvent(event, watcher) // watcher 也传递进去，用于动态添加/移除监视路径
			case errWatch, ok := <-watcher.Errors:
				if !ok {
					log.Printf("警告: [DBManager] 文件监视器错误通道已关闭。goroutine 退出。")
					return
				}
				log.Printf("错误: [DBManager] 文件监视器报告错误: %v", errWatch)
			}
		}
	}()
	return nil
}

// handleFsEvent 处理文件系统事件。
func (m *Manager) handleFsEvent(event fsnotify.Event, watcher *fsnotify.Watcher) {
	// 清理路径并获取相对于 m.root 的路径
	cleanPath := filepath.Clean(event.Name)
	relPath, errRel := filepath.Rel(m.root, cleanPath)
	if errRel != nil {
		log.Printf("警告: [DBManager FS Event] 无法获取事件路径 '%s' 的相对路径: %v", cleanPath, errRel)
		return
	}
	relPathSlash := filepath.ToSlash(relPath) // 统一使用 '/' 作为分隔符

	// 新业务组目录创建
	if event.Op.Has(fsnotify.Create) {
		fileInfo, errStat := os.Stat(cleanPath)
		if errStat == nil && fileInfo.IsDir() {
			// 检查是否是根目录下的直接子目录 (即 relPathSlash 不包含 '/')
			if !strings.Contains(relPathSlash, "/") {
				log.Printf("信息: [DBManager FS Event] 检测到新目录创建: '%s'。尝试添加到监视器。", cleanPath)
				if errAdd := watcher.Add(cleanPath); errAdd != nil {
					log.Printf("警告: [DBManager FS Event] 热添加新业务目录 '%s' 到监视器失败: %v", cleanPath, errAdd)
				} else {
					log.Printf("信息: [DBManager FS Event] 新业务目录 '%s' 已成功添加到监视器。", cleanPath)
				}
			}
			return // 目录事件处理完毕
		}
	}

	//.db 文件的创建、删除、重命名、写入
	// 我们只关心业务组目录下的 .db 文件 (e.g., instance/bizA/lib1.db)
	if !strings.HasSuffix(strings.ToLower(cleanPath), ".db") {
		return // 不是 .db 文件，忽略
	}
	// 确保是 bizName/libName.db 结构
	if strings.Count(relPathSlash, "/") != 1 {
		return // 路径层级不对，忽略
	}

	// 使用防抖处理 .db 文件事件
	m.eventTimersMu.Lock()
	if timer, exists := m.eventTimers[cleanPath]; exists {
		timer.Stop() // 如果已有计时器，则重置
	}
	log.Printf("调试: [DBManager FS Event] 收到 .db 文件事件: %s (Op: %s)。启动防抖计时器。", cleanPath, event.Op.String())
	m.eventTimers[cleanPath] = time.AfterFunc(debounceDuration, func() {
		m.processDebouncedEvent(cleanPath, event.Op) // 传递原始操作类型，以便更精确处理
		// 清理已执行的计时器
		m.eventTimersMu.Lock()
		delete(m.eventTimers, cleanPath)
		m.eventTimersMu.Unlock()
	})
	m.eventTimersMu.Unlock()
}

// processDebouncedEvent 在防抖后实际处理 .db 文件的变更。
func (m *Manager) processDebouncedEvent(path string, originalOp fsnotify.Op) {
	log.Printf("信息: [DBManager Debounced Event] 开始处理文件: '%s' (原始触发操作可能包含: %s)", path, originalOp.String())
	ctxBg := context.Background() // 使用后台context进行处理

	needsSchemaRefresh := false

	if originalOp.Has(fsnotify.Remove) || originalOp.Has(fsnotify.Rename) {
		log.Printf("信息: [DBManager Debounced Event] 文件 '%s' 被移除或重命名，执行关闭和清理。", path)
		m.closeDB(path) // closeDB 内部有锁
		needsSchemaRefresh = true
	}

	// 如果文件存在 (可能是CREATE, WRITE, 或RENAME后的新文件)
	// 即使是REMOVE事件，也检查一下，万一文件又回来了。
	// 但主要针对的是CREATE和WRITE。
	if _, err := os.Stat(path); err == nil { // 文件存在
		if originalOp.Has(fsnotify.Create) || originalOp.Has(fsnotify.Write) || originalOp.Has(fsnotify.Rename) {
			log.Printf("信息: [DBManager Debounced Event] 文件 '%s' 被创建/写入/重命名(新)，尝试重新加载。", path)
			// 先尝试关闭，以防是更新现有DB（虽然通常rename/remove事件会先处理）
			m.closeDB(path)                                       // 确保旧连接已关闭
			if errOpen := m.openDB(ctxBg, path); errOpen != nil { // openDB 内部有锁
				log.Printf("错误: [DBManager Debounced Event] 热加载/打开数据库 '%s' 失败: %v", path, errOpen)
			} else {
				log.Printf("信息: [DBManager Debounced Event] 热加载/打开数据库 '%s' 成功。", path)
				needsSchemaRefresh = true
			}
		}
	} else if os.IsNotExist(err) && !(originalOp.Has(fsnotify.Remove) || originalOp.Has(fsnotify.Rename)) {
		// 文件不存在，但原始事件不是Remove/Rename，这可能是一个短暂状态或竞争条件。
		// 如果它之前被加载过，尝试关闭。
		log.Printf("信息: [DBManager Debounced Event] 文件 '%s' 不存在，但原始事件不是移除/重命名。尝试确保其已关闭。", path)
		m.closeDB(path)
		needsSchemaRefresh = true // 即使文件没了，物理schema并集也可能变化
	}

	if needsSchemaRefresh {
		log.Printf("信息: [DBManager Debounced Event] 因文件事件 (路径: '%s'), 准备刷新业务组物理 schema 并集缓存 (m.schema)。", path)
		m.loadOrRefreshSchema() // 此方法内部有锁
	} else {
		log.Printf("调试: [DBManager Debounced Event] 文件事件 (路径: '%s')，此次未进行 m.schema 刷新。", path)
	}
}

/*
================================================================================
  业务组物理 Schema 并集缓存 (m.schema) 的加载与刷新
  这部分逻辑与 schema_cache.go 文件中的 readSchemaCache/writeSchemaCache 协作。
================================================================================
*/

// loadOrRefreshSchemaInternal 负责计算并更新 m.schema。
// m.schema 存储每个业务组下所有库的物理表及列的并集。
// 它会尝试从 schema_cache.json 加载，失败则全量扫描并回写。
// 调用此方法前必须获取写锁 m.mu.Lock()。
func (m *Manager) loadOrRefreshSchemaInternal() {
	log.Printf("信息: [DBManager] 开始刷新所有业务的 (物理) schema 并集缓存 (m.schema)...")
	newCombinedSchemaState := make(map[string]map[string][]string) // bizName -> tableName -> union_of_columnNames

	// 遍历当前Manager中加载的所有业务组
	for bizName, libsMapInBiz := range m.group { // libsMapInBiz is map[libName]*sql.DB
		bizDirPath := filepath.Join(m.root, bizName) // 例如: instance/bizA

		unionSchemaFromCache, _, errCache := readSchemaCache(bizDirPath)

		if errCache == nil && unionSchemaFromCache != nil { // 缓存有效
			newCombinedSchemaState[bizName] = unionSchemaFromCache
			log.Printf("信息: [DBManager] 业务 '%s' 的物理 schema 并集已从缓存文件 '%s' 加载。",
				bizName, filepath.Join(bizDirPath, schemaCacheFilename))
		} else { // 缓存不存在、读取失败或内容无效，则执行全量扫描
			if errCache != nil {
				log.Printf("警告: [DBManager] 业务 '%s' 读取物理 schema 并集缓存失败 (%v)，将执行全量扫描。", bizName, errCache)
			} else {
				log.Printf("信息: [DBManager] 业务 '%s' 物理 schema 并集缓存未找到或无效，将执行全量扫描。", bizName)
			}

			currentBizSchemaUnion := make(map[string][]string)             // 当前业务组下，所有库的表的列的并集
			currentBizPerLibSchema := make(map[string]map[string][]string) // 用于写入缓存：libName -> tableName -> columnNames

			if len(libsMapInBiz) == 0 {
				log.Printf("信息: [DBManager] 业务 '%s' 下当前没有加载任何数据库，无法扫描物理 schema。", bizName)
			} else {
				for libName, dbConn := range libsMapInBiz {
					phySchema, found := m.dbSchemaCache[dbConn]
					if !found || phySchema == nil || phySchema.allTablesAndColumns == nil {
						log.Printf("错误: [DBManager] 业务 '%s' 库 '%s' 的物理 schema 未在 dbSchemaCache 中找到或不完整，无法用于构建并集。", bizName, libName)
						continue // 跳过此库
					}

					// 记录此库的物理 schema，用于写入 schema_cache.json 的 "Libs" 部分
					currentBizPerLibSchema[libName] = phySchema.allTablesAndColumns

					// 将此库的物理 schema 合并到业务组的 schema 并集 (currentBizSchemaUnion)
					for tableName, columnsInLib := range phySchema.allTablesAndColumns {
						if existingColsInUnion, tableInUnion := currentBizSchemaUnion[tableName]; tableInUnion {
							mergedColsSet := make(map[string]struct{})
							for _, c := range existingColsInUnion {
								mergedColsSet[c] = struct{}{}
							}

							// 正确的循环，将当前库的列也加入到set中
							for _, colFromLib := range columnsInLib {
								mergedColsSet[colFromLib] = struct{}{}
							}

							finalColsSlice := make([]string, 0, len(mergedColsSet))
							for c_set := range mergedColsSet { // 从set中取出唯一的列名
								finalColsSlice = append(finalColsSlice, c_set)
							}
							sort.Strings(finalColsSlice) // 保证顺序
							currentBizSchemaUnion[tableName] = finalColsSlice
						} else {
							// 新表，直接添加 (columnsInLib 已在 loadDBPhysicalSchema 中排序)
							currentBizSchemaUnion[tableName] = columnsInLib
						}
					} // 结束 for tableName, columnsInLib
				} // 结束 for libName, dbConn
				newCombinedSchemaState[bizName] = currentBizSchemaUnion
				// 将扫描得到的物理 schema 并集和各库详情写入缓存文件
				if errWrite := writeSchemaCache(bizDirPath, currentBizPerLibSchema, currentBizSchemaUnion); errWrite != nil {
					log.Printf("错误: [DBManager] 业务 '%s' 扫描后写入物理 schema 缓存文件失败: %v", bizName, errWrite)
				} else {
					log.Printf("信息: [DBManager] 业务 '%s' 的物理 schema 并集已扫描并写入缓存文件 '%s'。",
						bizName, filepath.Join(bizDirPath, schemaCacheFilename))
				}
			}
		}
	}

	// 清理 m.schema 中那些在当前 m.group (即实际加载的业务组) 中已不存在的业务组条目
	for bizInOldSchema := range m.schema {
		if _, groupStillLoaded := m.group[bizInOldSchema]; !groupStillLoaded {
			// 确认业务组确实已从 m.group 中移除 (例如，其下所有 .db 文件都被删了)
			log.Printf("信息: [DBManager] 业务 '%s' 已不再加载，从 (物理) schema 并集缓存 (m.schema) 中移除其旧条目。", bizInOldSchema)
			// newCombinedSchemaState 中自然不会包含它，所以直接赋值即可
		}
	}

	m.schema = newCombinedSchemaState // 原子替换旧的 schema 缓存
	log.Printf("信息: [DBManager] 所有业务的 (物理) schema 并集缓存 (m.schema) 刷新完成。")
}

// loadOrRefreshSchema 是 loadOrRefreshSchemaInternal 的公开包装器，带锁。
func (m *Manager) loadOrRefreshSchema() {
	m.mu.Lock() // 写锁保护 m.schema 和 m.dbSchemaCache (虽然这里主要写m.schema，但依赖dbSchemaCache)
	defer m.mu.Unlock()
	m.loadOrRefreshSchemaInternal()
}

/*
================================================================================
  Manager 提供的公开 API 方法
================================================================================
*/

// Tables 返回指定业务组下所有物理表的名称列表（来自 m.schema 缓存的并集）。
// 这个接口用于向前端或调用者展示该业务组下有哪些“物理上可能存在”的表。
// 用户实际能否查询这些表，以及能查询哪些列，由 QueryAdminConfigService 的配置决定。
func (m *Manager) Tables(bizName string) []string {
	m.mu.RLock() // 读锁保护 m.schema
	defer m.mu.RUnlock()

	bizSchemaData, bizExists := m.schema[bizName] // m.schema is map[bizName]map[tableName][]cols_union
	if !bizExists || len(bizSchemaData) == 0 {
		return nil // 或者返回空slice: []string{}
	}

	tableNames := make([]string, 0, len(bizSchemaData))
	for tableName := range bizSchemaData {
		tableNames = append(tableNames, tableName)
	}
	sort.Strings(tableNames) // 保证返回的表名列表顺序稳定
	return tableNames
}

// PhysicalColumns 返回指定业务组和表名下的所有物理列名的并集（来自 m.schema 缓存）。
// 同样，这反映的是物理结构，而非管理员配置的可查询/可返回列。
func (m *Manager) PhysicalColumns(bizName, tableName string) []string {
	m.mu.RLock() // 读锁保护 m.schema
	defer m.mu.RUnlock()

	if bizSchemaData, bizExists := m.schema[bizName]; bizExists {
		if columns, tableInBizSchema := bizSchemaData[tableName]; tableInBizSchema {
			// m.schema 中的列已经是排序过的并集
			return columns
		}
	}
	return nil // 或者返回空slice: []string{}
}

// Summary 返回一个映射，表示每个业务组 (bizName) 下有哪些库文件 (libName)。
// 这个方法反映了文件系统的物理结构，保持不变。
func (m *Manager) Summary() map[string][]string {
	m.mu.RLock() // 读锁保护 m.group
	defer m.mu.RUnlock()

	summaryMap := make(map[string][]string, len(m.group))
	for bizName, libsInBiz := range m.group {
		if len(libsInBiz) > 0 {
			libNames := make([]string, 0, len(libsInBiz))
			for libName := range libsInBiz {
				libNames = append(libNames, libName)
			}
			sort.Strings(libNames) // 保证库名列表顺序稳定
			summaryMap[bizName] = libNames
		}
	}
	return summaryMap
}

// GetAnyDB 随机（实际上是第一个找到的）返回一个当前加载的 *sql.DB 连接实例。
// 主要用于一些不需要特定业务上下文的内部操作或测试。
func (m *Manager) GetAnyDB() (*sql.DB, error) {
	m.mu.RLock() // 读锁保护 m.group
	defer m.mu.RUnlock()

	for _, libsInBiz := range m.group {
		for _, dbConn := range libsInBiz {
			if dbConn != nil {
				return dbConn, nil
			}
		}
	}
	return nil, fmt.Errorf("系统中当前没有加载任何可用的数据库实例")
}

/*
================================================================================
  核心查询逻辑 (Manager.Query 和 buildSQL_new)
================================================================================
*/

// Param 定义单列的查询参数
type Param struct {
	Field string // 查询条件的字段名 (数据库原始字段名)
	Value string // 查询条件的值
	Logic string // 与下一个查询条件的逻辑关系 (应为大写的 "AND" 或 "OR")
	Fuzzy bool   // 是否模糊查询
}

// Query 支持跨库并发检索，并根据管理员配置进行权限校验和结果构造。
func (m *Manager) Query(
	ctx context.Context,
	bizName string, // 业务组名称
	tableNameOrEmpty string, // 用户请求的表名，如果为空，则尝试使用业务组的默认表
	queryParams []Param, // 查询参数列表
	page int, // 页码 (1-based)
	size int, // 每页大小
) ([]map[string]any, error) {

	// 从配置服务获取该业务组的查询配置
	bizAdminConfig, err := m.configService.GetBizQueryConfig(ctx, bizName)
	if err != nil {
		log.Printf("错误: [DBManager Query] 获取业务 '%s' 的查询配置失败: %v", bizName, err)
		return nil, fmt.Errorf("业务 '%s' 查询配置不可用或获取失败", bizName)
	}
	if bizAdminConfig == nil {
		log.Printf("信息: [DBManager Query] 业务 '%s' 未找到对应的查询配置。", bizName)
		return nil, fmt.Errorf("业务 '%s' 未配置查询规则", bizName)
	}
	if !bizAdminConfig.IsPubliclySearchable {
		log.Printf("信息: [DBManager Query] 业务 '%s' (配置名: '%s') 已配置，但未开放公开查询。", bizName, bizAdminConfig.BizName)
		return nil, ErrPermissionDenied // 返回特定错误类型
	}

	// 确定实际查询的表名
	targetTableName := tableNameOrEmpty
	if targetTableName == "" {
		targetTableName = bizAdminConfig.DefaultQueryTable
	}
	if targetTableName == "" {
		log.Printf("错误: [DBManager Query] 业务 '%s' 查询请求未指定表名，且该业务未配置默认查询表。", bizName)
		return nil, fmt.Errorf("业务 '%s' 未能确定查询目标表，请指定表名或由管理员配置默认表", bizName)
	}

	// 获取目标表的具体配置
	tableAdminConfig, tableConfigExists := bizAdminConfig.Tables[targetTableName]
	if !tableConfigExists {
		log.Printf("错误: [DBManager Query] 表 '%s' 在业务 '%s' 的查询配置中未定义。", targetTableName, bizName)
		return nil, ErrTableNotFoundInBiz
	}
	if !tableAdminConfig.IsSearchable {
		log.Printf("信息: [DBManager Query] 表 '%s' 在业务 '%s' 中已配置，但管理员设定其为不可查询。", targetTableName, bizName)
		return nil, ErrPermissionDenied
	}

	// 校验并准备查询参数
	validatedQueryParams := make([]Param, 0, len(queryParams))
	if len(queryParams) > 0 {
		for i, p := range queryParams {
			if p.Field == "" {
				return nil, fmt.Errorf("查询条件字段名不能为空")
			}
			fieldSetting, fieldExists := tableAdminConfig.Fields[p.Field]
			if !fieldExists || !fieldSetting.IsSearchable {
				return nil, fmt.Errorf("查询条件字段 '%s' 无效或不被允许用于搜索", p.Field)
			}

			paramIsLast := (i == len(queryParams)-1)
			if !paramIsLast {
				p.Logic = strings.ToUpper(p.Logic)
				if p.Logic != "AND" && p.Logic != "OR" {
					return nil, fmt.Errorf("查询参数逻辑操作符 '%s' 无效", p.Logic)
				}
			} else {
				p.Logic = ""
			}
			validatedQueryParams = append(validatedQueryParams, p)
		}
	}

	defaultView, err := m.configService.GetDefaultViewConfig(ctx, bizName, targetTableName)
	if err != nil {
		log.Printf("错误: [DBManager Query] 获取表 '%s' 的默认视图配置失败: %v", targetTableName, err)
		return nil, fmt.Errorf("获取视图配置失败，无法继续查询")
	}
	if defaultView == nil {
		log.Printf("错误: [DBManager Query] 表 '%s' (业务 '%s') 没有配置默认视图。", targetTableName, bizName)
		return nil, fmt.Errorf("表 '%s' 没有可用的默认视图，请联系管理员配置", targetTableName)
	}
	if defaultView.ViewType != "table" && defaultView.ViewType != "cards" {
		// 当前查询只为表格和卡片视图提供数据
		log.Printf("错误: [DBManager Query] 表 '%s' 的默认视图类型 '%s' 不支持数据查询。", targetTableName, defaultView.ViewType)
		return nil, fmt.Errorf("默认视图类型 '%s' 不支持查询", defaultView.ViewType)
	}

	// 根据视图配置，构建 SELECT 列和别名映射
	selectFieldsForSQL := make([]string, 0)
	dbFieldToAliasMap := make(map[string]string)

	// 我们需要从数据库 SELECT 所有在视图中定义的字段，无论是卡片还是表格。为此，我们先将所有需要的字段名收集到一个set中去重
	requiredFieldsSet := make(map[string]string) // key: 数据库字段名, value: 显示名/别名

	if defaultView.Binding.Table != nil && len(defaultView.Binding.Table.Columns) > 0 {
		for _, col := range defaultView.Binding.Table.Columns {
			if col.Field != "" {
				requiredFieldsSet[col.Field] = col.DisplayName
			}
		}
	}
	if defaultView.Binding.Card != nil {
		cardBinding := defaultView.Binding.Card
		if cardBinding.Title != "" {
			requiredFieldsSet[cardBinding.Title] = ""
		} // 卡片视图通常不需要别名
		if cardBinding.Subtitle != "" {
			requiredFieldsSet[cardBinding.Subtitle] = ""
		}
		if cardBinding.Description != "" {
			requiredFieldsSet[cardBinding.Description] = ""
		}
		if cardBinding.ImageUrl != "" {
			requiredFieldsSet[cardBinding.ImageUrl] = ""
		}
		if cardBinding.Tag != "" {
			requiredFieldsSet[cardBinding.Tag] = ""
		}
	}

	if len(requiredFieldsSet) == 0 {
		log.Printf("错误: [DBManager Query] 表 '%s' (业务 '%s') 的默认视图未绑定任何字段。", targetTableName, bizName)
		return nil, fmt.Errorf("默认视图未配置任何展示字段")
	}

	// 校验视图中定义的字段是否都允许返回
	for dbField, displayName := range requiredFieldsSet {
		fieldSetting, fieldExists := tableAdminConfig.Fields[dbField]
		if !fieldExists || !fieldSetting.IsReturnable {
			log.Printf("错误: [DBManager Query] 视图配置尝试返回一个不可返回的字段 '%s'", dbField)
			return nil, fmt.Errorf("安全策略冲突：视图配置尝试访问未授权返回的字段 '%s'", dbField)
		}
		selectFieldsForSQL = append(selectFieldsForSQL, dbField)
		if displayName != "" {
			dbFieldToAliasMap[dbField] = displayName
		}
	}
	sort.Strings(selectFieldsForSQL) // 排序以保证 SELECT 子句的稳定性

	m.mu.RLock()
	dbInstancesInBiz, bizGroupExists := m.group[bizName]
	m.mu.RUnlock()
	if !bizGroupExists || len(dbInstancesInBiz) == 0 {
		return []map[string]any{}, nil
	}

	sem := make(chan struct{}, runtime.NumCPU())
	resultsChannel := make(chan []map[string]any, len(dbInstancesInBiz))
	errGroup, queryCtx := errgroup.WithContext(ctx)

	processedLibsCount := 0
	for libName, dbConn := range dbInstancesInBiz {
		m.mu.RLock()
		physicalSchemaInfo, hasPhysicalSchema := m.dbSchemaCache[dbConn]
		m.mu.RUnlock()
		if !hasPhysicalSchema {
			continue
		}
		if _, tablePhysicallyExists := physicalSchemaInfo.allTablesAndColumns[targetTableName]; !tablePhysicallyExists {
			continue
		}

		processedLibsCount++
		currentLibName := libName
		currentDBConn := dbConn

		errGroup.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-queryCtx.Done():
				return queryCtx.Err()
			}

			sqlQuery, queryArgs, errBuild := buildsqlNew(
				targetTableName,
				selectFieldsForSQL,
				dbFieldToAliasMap,
				validatedQueryParams,
				page,
				size,
			)
			if errBuild != nil {
				return nil
			}

			log.Printf("调试: [DBManager Query] 执行SQL for %s/%s, Table: %s. SQL: %s, Args: %v", bizName, currentLibName, targetTableName, sqlQuery, queryArgs)
			rows, errExec := currentDBConn.QueryContext(queryCtx, sqlQuery, queryArgs...)
			if errExec != nil {
				return fmt.Errorf("查询库 '%s/%s' 表 '%s' 失败: %w", bizName, currentLibName, targetTableName, errExec)
			}
			defer rows.Close()

			actualReturnedColumns, _ := rows.Columns()
			var libResults []map[string]any
			for rows.Next() {
				scanDest := make([]any, len(actualReturnedColumns))
				scanDestPtrs := make([]any, len(actualReturnedColumns))
				for i := range scanDest {
					scanDestPtrs[i] = &scanDest[i]
				}
				if errScan := rows.Scan(scanDestPtrs...); errScan != nil {
					return fmt.Errorf("扫描库 '%s/%s' 表 '%s' 行数据失败: %w", bizName, currentLibName, targetTableName, errScan)
				}
				rowData := make(map[string]any)
				rowData["__lib"] = currentLibName
				for i, colName := range actualReturnedColumns {
					if bytes, ok := scanDest[i].([]byte); ok {
						rowData[colName] = string(bytes)
					} else {
						rowData[colName] = scanDest[i]
					}
				}
				libResults = append(libResults, rowData)
			}
			if errRows := rows.Err(); errRows != nil {
				return fmt.Errorf("迭代库 '%s/%s' 表 '%s' 行数据失败: %w", bizName, currentLibName, targetTableName, errRows)
			}
			if len(libResults) > 0 {
				resultsChannel <- libResults
			}
			return nil
		})
	}

	var firstErrorEncountered error
	go func() {
		firstErrorEncountered = errGroup.Wait()
		close(resultsChannel)
	}()

	allAggregatedResults := make([]map[string]any, 0)
	for resSlice := range resultsChannel {
		allAggregatedResults = append(allAggregatedResults, resSlice...)
	}

	if firstErrorEncountered != nil {
		log.Printf("错误: [DBManager Query] 业务 '%s' 表 '%s' 的并发查询中至少一个库操作失败: %v", bizName, targetTableName, firstErrorEncountered)
		return allAggregatedResults, fmt.Errorf("查询业务 '%s' 的表 '%s' 时发生部分错误: %w", bizName, targetTableName, firstErrorEncountered)
	}

	return allAggregatedResults, nil
}

// buildsqlNew 根据管理员配置动态构建SQL语句
func buildsqlNew(
	tableName string,
	selectDBFields []string, // 需要从数据库 SELECT 的原始字段名列表 (已验证为 IsReturnable)
	fieldAliases map[string]string, // 原始字段名 -> API响应中的别名
	queryParams []Param, // 经过校验的查询条件 (字段已验证为 IsSearchable, Logic已验证或为空)
	page int, // 页码 (1-based)
	size int, // 每页大小
) (sqlQuery string, args []any, err error) {

	// --- 基本校验 ---
	if tableName == "" {
		return "", nil, fmt.Errorf("表名不能为空 (buildSQL_new)")
	}
	if len(selectDBFields) == 0 {
		return "", nil, fmt.Errorf("无可返回的列 (buildSQL_new)")
	}
	if page < 1 {
		log.Printf("调试: [buildSQL_new] 无效的页码 %d，修正为 1。", page)
		page = 1
	}
	if size < 1 || size > 1000 { // 假设最大页大小为1000，可配置
		log.Printf("调试: [buildSQL_new] 无效的页大小 %d，修正为默认值 50。", size)
		size = 50 // 默认且合理的大小
	}

	selectClauseParts := make([]string, len(selectDBFields))
	for i, dbField := range selectDBFields {
		alias, hasAlias := fieldAliases[dbField]
		if hasAlias && alias != "" {
			selectClauseParts[i] = fmt.Sprintf("%q AS %q", dbField, alias)
		} else {
			selectClauseParts[i] = fmt.Sprintf("%q", dbField)
		}
	}
	selectClause := strings.Join(selectClauseParts, ", ")

	var whereConditions []string
	var queryArgsForWhere []any

	if len(queryParams) > 0 {
		for i, p := range queryParams {
			var operator string
			var valueToBind any
			if p.Fuzzy {
				operator = "LIKE"
				likeValue := strings.ReplaceAll(p.Value, `\`, `\\`) // 先转义反斜杠本身
				likeValue = strings.ReplaceAll(likeValue, `%`, `\%`)
				likeValue = strings.ReplaceAll(likeValue, `_`, `\_`)
				valueToBind = "%" + likeValue + "%"
			} else {
				operator = "="
				valueToBind = p.Value
			}
			whereConditions = append(whereConditions, fmt.Sprintf("%q %s ?", p.Field, operator))
			queryArgsForWhere = append(queryArgsForWhere, valueToBind)

			if i < len(queryParams)-1 { // 如果不是最后一个条件
				// p.Logic 应已由 Manager.Query 校验为 "AND" 或 "OR" (或为空，表示默认处理)
				if p.Logic == "AND" || p.Logic == "OR" {
					whereConditions = append(whereConditions, p.Logic)
				} else if p.Logic == "" {
					log.Printf("调试: [buildSQL_new] 查询参数 %d 的 Logic 为空，且不是最后一个参数。这可能导致SQL语法问题，除非只有一个查询参数。", i)
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(selectClause)
	sb.WriteString(fmt.Sprintf(" FROM %q", tableName))

	if len(whereConditions) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(whereConditions, " "))
	}

	sb.WriteString(" LIMIT ? OFFSET ?")

	sqlQuery = sb.String()
	args = append(queryArgsForWhere, size, (page-1)*size)

	return sqlQuery, args, nil
}
