// Package sqlite 提供对 SQLite 数据库的访问与管理功能
// 文件位置: internal/adapter/datasource/sqlite/db_ops.go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

// InitForBiz 初始化指定业务组下的所有数据库文件
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

// openDBInternal 打开并注册指定路径的数据库连接
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
		m.group[bizName] = make(map[string]*dbInstance)
	}

	m.group[bizName][libName] = &dbInstance{
		conn: db,
		path: path,
	}
	m.dbSchemaCache[db] = phySchema

	log.Printf("信息: [DBManager] 成功打开并加载数据库: %s/%s", bizName, libName)
	return nil
}

// openDB 为 openDBInternal 提供线程安全封装
func (m *Manager) openDB(ctx context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.openDBInternal(ctx, path)
}

// closeDB 关闭指定路径的数据库连接并清理缓存
func (m *Manager) closeDB(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rel, errRel := filepath.Rel(m.root, path)
	if errRel != nil {
		return
	}
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
	if len(parts) != 2 {
		return
	}
	bizName, fileName := parts[0], parts[1]
	libName := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	if bizGroup, bizExists := m.group[bizName]; bizExists {
		if instance, libExists := bizGroup[libName]; libExists {
			delete(m.dbSchemaCache, instance.conn)
			if errClose := instance.conn.Close(); errClose != nil {
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

// HealthCheck 检查插件系统数据库连接是否可用
func (m *Manager) HealthCheck(ctx context.Context) error {
	if m.pluginSysDB == nil {
		return fmt.Errorf("系统数据库连接未初始化")
	}
	return m.pluginSysDB.PingContext(ctx)
}

// getAnyDB 返回当前加载的任意一个数据库连接实例
func (m *Manager) getAnyDB() (*sql.DB, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, libsInBiz := range m.group {
		for _, instance := range libsInBiz {
			if instance != nil && instance.conn != nil {
				return instance.conn, nil
			}
		}
	}
	return nil, fmt.Errorf("系统中当前没有加载任何可用的数据库实例")
}
