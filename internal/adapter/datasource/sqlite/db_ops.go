// Package sqlite file: internal/adapter/datasource/sqlite/db_ops.go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

// InitForBiz 根据指定的业务组名称，精确地初始化该业务组下的所有数据库。
func (m *Manager) InitForBiz(ctx context.Context, rootDir string, bizName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.root == "" {
		m.root = filepath.Clean(rootDir)
	}

	bizPath := filepath.Join(m.root, bizName)
	globPattern := filepath.Join(bizPath, "*.db")
	log.Printf("[DBManager] 开始为业务组 '%s' 初始化, 扫描模式: %s", bizName, globPattern)

	files, err := filepath.Glob(globPattern)
	if err != nil {
		return fmt.Errorf("为业务组 '%s' 扫描数据库目录失败: %w", bizName, err)
	}

	if len(files) == 0 {
		log.Printf("信息: [DBManager] 在业务组 '%s' 的目录 '%s' 下未找到任何 '.db' 文件。", bizName, bizPath)
		m.loadOrRefreshSchemaInternal()
		return nil
	}

	var loadedCount int
	for _, f := range files {
		if errOpen := m.openDBInternal(ctx, f); errOpen != nil {
			log.Printf("警告: [DBManager] 初始化时打开数据库 '%s' 失败: %v", f, errOpen)
		} else {
			loadedCount++
		}
	}

	log.Printf("[DBManager] 业务组 '%s' 初始化完成。成功加载 %d 个数据库。", bizName, loadedCount)
	m.loadOrRefreshSchemaInternal()
	return nil
}

// openDBInternal 是打开单个数据库文件、加载其物理schema并更新Manager内部状态的私有方法。
// 调用前必须获取写锁。
func (m *Manager) openDBInternal(ctx context.Context, path string) error {
	rel, errRel := filepath.Rel(m.root, path)
	if errRel != nil {
		return fmt.Errorf("无法获取文件 '%s' 的相对路径: %w", path, errRel)
	}

	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("非法数据库路径结构 (应为 <bizName>/<libName>.db): '%s'", rel)
	}
	bizName, fileName := parts[0], parts[1]
	libName := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=ON", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("sql.Open '%s' 失败: %w", path, err)
	}

	if errPing := db.PingContext(ctx); errPing != nil {
		_ = db.Close()
		return fmt.Errorf("ping 数据库 '%s' 失败: %w", path, errPing)
	}

	phySchema, errLoad := loadDBPhysicalSchema(ctx, db)
	if errLoad != nil {
		_ = db.Close()
		return fmt.Errorf("加载数据库 '%s' 的物理 schema 失败: %w", path, errLoad)
	}

	if m.group[bizName] == nil {
		m.group[bizName] = make(map[string]*sql.DB)
	}
	m.group[bizName][libName] = db
	m.dbSchemaCache[db] = phySchema

	log.Printf("信息: [DBManager] 成功打开并加载数据库: %s/%s", bizName, libName)
	return nil
}

// openDB 是 openDBInternal 的公开包装器，带锁。
func (m *Manager) openDB(ctx context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.openDBInternal(ctx, path)
}

// closeDB 关闭指定路径的数据库连接，并清理相关缓存。带锁。
func (m *Manager) closeDB(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rel, errRel := filepath.Rel(m.root, path)
	if errRel != nil {
		return // Path is not relative to root, nothing to do.
	}
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
	if len(parts) != 2 {
		return
	}
	bizName, fileName := parts[0], parts[1]
	libName := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	if bizGroup, bizExists := m.group[bizName]; bizExists {
		if db, libExists := bizGroup[libName]; libExists {
			delete(m.dbSchemaCache, db)
			if errClose := db.Close(); errClose != nil {
				log.Printf("警告: [DBManager] 关闭数据库 %s/%s 时发生错误: %v", bizName, libName, errClose)
			} else {
				log.Printf("信息: [DBManager] 成功关闭数据库: %s/%s", bizName, libName)
			}
			delete(bizGroup, libName)
			if len(bizGroup) == 0 {
				delete(m.group, bizName)
				delete(m.schema, bizName)
			}
		}
	}
}

// HealthCheck 实现 port.DataSource.HealthCheck
func (m *Manager) HealthCheck(ctx context.Context) error {
	db, err := m.getAnyDB()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

// getAnyDB 随机返回一个当前加载的 *sql.DB 连接实例。
func (m *Manager) getAnyDB() (*sql.DB, error) {
	m.mu.RLock()
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
