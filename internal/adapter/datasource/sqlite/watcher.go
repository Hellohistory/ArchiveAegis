// Package sqlite file: internal/adapter/datasource/sqlite/watcher.go
package sqlite

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// StartWatcher 启动文件系统监视器，用于热加载/卸载数据库。
func (m *Manager) StartWatcher(rootDir string) error {
	if m.root == "" {
		m.root = filepath.Clean(rootDir)
	}
	log.Printf("[DBManager] 尝试启动文件监视器于目录: %s", m.root)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("创建 fsnotify watcher 失败: %w", err)
	}

	// Goroutine to handle events
	go func() {
		defer watcher.Close()
		log.Printf("信息: [DBManager] 文件监视 goroutine 已启动。")
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					log.Printf("警告: [DBManager] 文件监视器事件通道已关闭。")
					return
				}
				m.handleFsEvent(event, watcher)
			case errWatch, ok := <-watcher.Errors:
				if !ok {
					log.Printf("警告: [DBManager] 文件监视器错误通道已关闭。")
					return
				}
				log.Printf("错误: [DBManager] 文件监视器报告错误: %v", errWatch)
			}
		}
	}()

	// Add root directory to watch for new biz folders
	if err := watcher.Add(m.root); err != nil {
		log.Printf("错误: [DBManager] 添加根目录 '%s' 到监视器失败: %v", m.root, err)
	} else {
		log.Printf("信息: [DBManager] 已成功添加根目录 '%s' 到监视器。", m.root)
	}

	// Add existing biz directories
	m.mu.RLock()
	for bizName := range m.group {
		bizPath := filepath.Join(m.root, bizName)
		if err := watcher.Add(bizPath); err != nil {
			log.Printf("警告: [DBManager] 添加现有业务目录 '%s' 到监视器失败: %v。", bizPath, err)
		}
	}
	m.mu.RUnlock()

	return nil
}

// handleFsEvent 处理单个文件系统事件。
func (m *Manager) handleFsEvent(event fsnotify.Event, watcher *fsnotify.Watcher) {
	cleanPath := filepath.Clean(event.Name)

	// Handle new directory creation (potentially a new biz group)
	if event.Op.Has(fsnotify.Create) {
		if info, err := os.Stat(cleanPath); err == nil && info.IsDir() {
			if err := watcher.Add(cleanPath); err == nil {
				log.Printf("信息: [DBManager FS Event] 新业务目录 '%s' 已成功添加到监视器。", cleanPath)
			}
			return
		}
	}

	// We only care about .db files for hot-reloading
	if !strings.HasSuffix(strings.ToLower(cleanPath), ".db") {
		return
	}

	// Debounce the event to handle rapid changes gracefully
	m.eventTimersMu.Lock()
	defer m.eventTimersMu.Unlock()
	if timer, exists := m.eventTimers[cleanPath]; exists {
		timer.Stop()
	}
	m.eventTimers[cleanPath] = time.AfterFunc(debounceDuration, func() {
		m.processDebouncedEvent(cleanPath)
		m.eventTimersMu.Lock()
		delete(m.eventTimers, cleanPath)
		m.eventTimersMu.Unlock()
	})
}

// processDebouncedEvent 在防抖后实际处理 .db 文件的变更。
func (m *Manager) processDebouncedEvent(path string) {
	log.Printf("信息: [DBManager Debounced Event] 开始处理文件: '%s'", path)
	ctxBg := context.Background()
	needsSchemaRefresh := false

	// If file no longer exists, it was removed or renamed.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		m.closeDB(path)
		needsSchemaRefresh = true
	} else {
		// File exists, so it was created or modified.
		// We perform a close & open to ensure we have the latest version.
		m.closeDB(path)
		if errOpen := m.openDB(ctxBg, path); errOpen != nil {
			log.Printf("错误: [DBManager Debounced Event] 热加载数据库 '%s' 失败: %v", path, errOpen)
		} else {
			log.Printf("信息: [DBManager Debounced Event] 热加载数据库 '%s' 成功。", path)
			needsSchemaRefresh = true
		}
	}

	if needsSchemaRefresh {
		log.Printf("信息: [DBManager Debounced Event] 因 '%s' 的文件事件，准备刷新 Schema 缓存。", path)
		m.loadOrRefreshSchema()
	}
}
