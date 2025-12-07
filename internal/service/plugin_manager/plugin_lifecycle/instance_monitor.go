// Package plugin_lifecycle 提供插件实例的生命周期监控功能
// file: internal/service/plugin_manager/plugin_lifecycle/instance_monitor.go
package plugin_lifecycle

import (
	"ArchiveAegis/internal/core/domain"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// registerAndMonitorPlugin 连接插件、注册执行器，并启动监控插件进程。
func (lm *LifecycleManager) registerAndMonitorPlugin(cmd *exec.Cmd, instanceID string, inst domain.PluginInstance) {
	address := fmt.Sprintf("localhost:%d", inst.Port)
	adapter, protocolVersion, err := lm.tryConnectPlugin(address, instanceID)
	if err != nil {
		if errors.Is(err, ErrIncompatibleProtocol) {
			log.Printf("❌ [LifecycleManager] 插件实例 '%s' 协议不兼容: %v", instanceID, err)
		} else {
			log.Printf("⚠️ [LifecycleManager] 插件实例 '%s' 握手失败: %v", instanceID, err)
		}
		lm.runningInstancesMu.Lock()
		if state, ok := lm.runningInstances[instanceID]; ok {
			state.executor = nil
			state.adapter = nil
			state.healthStatus = HealthStatusUnreachable
			state.protocolVersion = ""
		}
		lm.runningInstancesMu.Unlock()
		_, _ = lm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", StatusError, instanceID)
		if stopErr := lm.Stop(instanceID); stopErr != nil && !errors.Is(stopErr, ErrInstanceNotRunning) {
			log.Printf("⚠️ [LifecycleManager] 自动停止失败的插件 '%s' 时出错: %v", instanceID, stopErr)
		}
		return
	}

	lm.runningInstancesMu.Lock()
	if ri, ok := lm.runningInstances[instanceID]; ok {
		ri.executor = adapter
		ri.adapter = adapter
		ri.protocolVersion = protocolVersion
		ri.lastHeartbeat = time.Now()
		ri.healthStatus = HealthStatusHealthy
		ri.failureCount = 0
		ri.circuitOpenUntil = time.Time{}
		ri.autoRecoveryAttempts = 0
		if inst.BizName != "" {
			lm.executorRegistry[inst.BizName] = adapter
		}
		*lm.closableAdapters = append(*lm.closableAdapters, adapter)
	} else {
		lm.runningInstancesMu.Unlock()
		_ = adapter.Close()
		return
	}
	lm.runningInstancesMu.Unlock()

	go lm.monitorPlugin(cmd, instanceID)
}

// monitorPlugin 监控插件进程退出并执行后续清理操作。
func (lm *LifecycleManager) monitorPlugin(cmd *exec.Cmd, instanceID string) {
	err := cmd.Wait()
	if err != nil {
		log.Printf("⚠️ [LifecycleManager] 插件实例 '%s' 异常退出: %v", instanceID, err)
	} else {
		log.Printf("ℹ️ [LifecycleManager] 插件实例 '%s' 已退出", instanceID)
	}

	lm.cleanupAfterStop(instanceID)

	if err == nil {
		return
	}

	lm.runningInstancesMu.Lock()
	state, ok := lm.runningInstances[instanceID]
	if !ok {
		lm.runningInstancesMu.Unlock()
		return
	}
	state.healthStatus = HealthStatusUnreachable
	state.circuitOpenUntil = time.Now().Add(circuitOpenDuration)
	delay := time.Until(state.circuitOpenUntil)
	lm.runningInstancesMu.Unlock()
	go lm.scheduleRecovery(instanceID, delay)

	_, _ = lm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", StatusError, instanceID)
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
	ids := make([]string, 0, len(lm.runningInstances))
	now := time.Now()
	for id, state := range lm.runningInstances {
		if state == nil || state.executor == nil {
			continue
		}
		if !state.circuitOpenUntil.IsZero() && now.Before(state.circuitOpenUntil) {
			continue
		}
		ids = append(ids, id)
	}
	lm.runningInstancesMu.RUnlock()

	for _, instanceID := range ids {
		go lm.checkPluginHealth(instanceID)
	}
}

// checkPluginHealth 检查单个插件实例的健康状态，失败时标记并停止。
func (lm *LifecycleManager) checkPluginHealth(instanceID string) {
	lm.runningInstancesMu.RLock()
	state, ok := lm.runningInstances[instanceID]
	if !ok || state.executor == nil || state.cmd == nil {
		lm.runningInstancesMu.RUnlock()
		return
	}
	executor := state.executor
	lm.runningInstancesMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := executor.HealthCheck(ctx); err != nil {
		log.Printf("⚠️ [LifecycleManager] 插件实例 '%s' 健康检查失败: %v", instanceID, err)
		lm.runningInstancesMu.Lock()
		if st, exists := lm.runningInstances[instanceID]; exists {
			st.failureCount++
			st.healthStatus = HealthStatusDegraded
			failures := st.failureCount
			lm.runningInstancesMu.Unlock()
			if failures >= healthFailureThreshold {
				log.Printf("⛔ [LifecycleManager] 插件实例 '%s' 多次健康检查失败，执行熔断", instanceID)
				_, _ = lm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", StatusError, instanceID)
				if stopErr := lm.Stop(instanceID); stopErr != nil && !errors.Is(stopErr, ErrInstanceNotRunning) {
					log.Printf("⚠️ [LifecycleManager] 熔断时停止实例 '%s' 失败: %v", instanceID, stopErr)
				}
			}
		} else {
			lm.runningInstancesMu.Unlock()
		}
		return
	}

	lm.runningInstancesMu.Lock()
	if st, exists := lm.runningInstances[instanceID]; exists {
		st.failureCount = 0
		st.lastHeartbeat = time.Now()
		st.healthStatus = HealthStatusHealthy
	}
	lm.runningInstancesMu.Unlock()
}

func (lm *LifecycleManager) scheduleRecovery(instanceID string, delay time.Duration) {
	if delay <= 0 {
		delay = 5 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C

	lm.runningInstancesMu.Lock()
	state, ok := lm.runningInstances[instanceID]
	if !ok {
		lm.runningInstancesMu.Unlock()
		return
	}
	if time.Now().Before(state.circuitOpenUntil) {
		lm.runningInstancesMu.Unlock()
		return
	}
	if state.autoRecoveryAttempts >= maxRecoveryAttempts {
		lm.runningInstancesMu.Unlock()
		log.Printf("🚫 [LifecycleManager] 插件实例 '%s' 自动恢复次数达到上限。", instanceID)
		return
	}
	state.autoRecoveryAttempts++
	attempt := state.autoRecoveryAttempts
	state.healthStatus = HealthStatusRecovering
	lm.runningInstancesMu.Unlock()

	log.Printf("🛠️ [LifecycleManager] 开始自动恢复插件实例 '%s' (第 %d 次)", instanceID, attempt)

	if err := lm.Start(instanceID); err != nil {
		log.Printf("❌ [LifecycleManager] 自动恢复实例 '%s' 失败: %v", instanceID, err)
		lm.runningInstancesMu.Lock()
		if st, exists := lm.runningInstances[instanceID]; exists {
			st.healthStatus = HealthStatusUnreachable
			if st.autoRecoveryAttempts < maxRecoveryAttempts {
				st.circuitOpenUntil = time.Now().Add(circuitOpenDuration)
				nextDelay := time.Until(st.circuitOpenUntil)
				lm.runningInstancesMu.Unlock()
				go lm.scheduleRecovery(instanceID, nextDelay)
				return
			}
			log.Printf("🚫 [LifecycleManager] 插件实例 '%s' 自动恢复次数达到上限。", instanceID)
		}
		lm.runningInstancesMu.Unlock()
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
