// Package plugin_lifecycle 提供插件实例的生命周期监控功能
// file: internal/service/plugin_manager/plugin_lifecycle/instance_monitor.go
package plugin_lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// registerAndMonitorPlugin 连接插件、注册执行器，并启动监控插件进程。
func (lm *LifecycleManager) registerAndMonitorPlugin(cmd *exec.Cmd, instanceID, address, bizName string) {
	adapter, err := lm.tryConnectPlugin(address, instanceID)
	if err != nil {
		if stopErr := lm.Stop(instanceID); stopErr != nil {
			log.Printf("⚠️ [LifecycleManager] 自动停止失败的插件 '%s' 时出错: %v", instanceID, stopErr)
		}
		return
	}

	lm.runningInstancesMu.Lock()
	if ri, ok := lm.runningInstances[instanceID]; ok {
		ri.executor = adapter
		ri.adapter = adapter
		lm.executorRegistry[bizName] = adapter
		*lm.closableAdapters = append(*lm.closableAdapters, adapter)
	} else {
		adapter.Close()
	}
	lm.runningInstancesMu.Unlock()

	go lm.monitorPlugin(cmd, instanceID)
}

// monitorPlugin 监控插件进程退出并执行后续清理操作。
func (lm *LifecycleManager) monitorPlugin(cmd *exec.Cmd, instanceID string) {
	_ = cmd.Wait()
	lm.cleanupAfterStop(instanceID)
}

// StartHealthChecks 启动后台定时任务，周期性执行健康检查。
func (lm *LifecycleManager) StartHealthChecks(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			<-ticker.C
			lm.performAllHealthChecks()
		}
	}()
}

// performAllHealthChecks 对所有运行中的插件实例执行一轮健康检查。
func (lm *LifecycleManager) performAllHealthChecks() {
	lm.runningInstancesMu.RLock()
	if len(lm.runningInstances) == 0 {
		lm.runningInstancesMu.RUnlock()
		return
	}
	registrySnapshot := make(map[string]string, len(lm.runningInstances))
	for id, ri := range lm.runningInstances {
		if ri.executor != nil && ri.bizName != "" {
			registrySnapshot[id] = ri.bizName
		}
	}
	lm.runningInstancesMu.RUnlock()

	if len(registrySnapshot) == 0 {
		return
	}

	for instanceID, bizName := range registrySnapshot {
		go lm.checkPluginHealth(instanceID, bizName)
	}
}

// checkPluginHealth 检查单个插件实例的健康状态，失败时标记并停止。
func (lm *LifecycleManager) checkPluginHealth(instanceID, bizName string) {
	lm.runningInstancesMu.RLock()
	ri, ok := lm.runningInstances[instanceID]
	if !ok || ri.executor == nil {
		lm.runningInstancesMu.RUnlock()
		return
	}
	executor := ri.executor
	lm.runningInstancesMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := executor.HealthCheck(ctx); err != nil {
		_, _ = lm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", StatusError, instanceID)
		_ = lm.Stop(instanceID)
	}
}

// ReconcileOrphanedPlugins 清理所有未被生命周期管理器追踪但仍在运行的插件进程。
func (lm *LifecycleManager) ReconcileOrphanedPlugins() error {
	rows, err := lm.db.Query(`SELECT instance_id FROM plugin_instances`)
	if err != nil {
		return fmt.Errorf("无法查询所有实例ID: %w", err)
	}
	defer rows.Close()

	var orphanedCount int
	for rows.Next() {
		var instanceID string
		if err := rows.Scan(&instanceID); err != nil {
			continue
		}

		pidPath, err := lm.getInstanceAssetPath(instanceID, "pid")
		if err != nil {
			continue
		}

		pid, err := readPIDFile(pidPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			continue
		}

		process, _ := os.FindProcess(pid)
		if err = process.Signal(syscall.Signal(0)); err == nil {
			_ = process.Kill()
			orphanedCount++
		}

		cleanupPIDFile(pidPath)
	}

	_, err = lm.db.Exec(`UPDATE plugin_instances SET status = ? WHERE status != ?`, StatusStopped, StatusStopped)
	if err != nil {
		return fmt.Errorf("重置插件状态失败: %w", err)
	}

	return rows.Err()
}
