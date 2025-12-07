// Package plugin_lifecycle 提供插件实例的生命周期管理功能
// file: internal/service/plugin_manager/plugin_lifecycle/instance_crud.go
package plugin_lifecycle

import (
	"ArchiveAegis/internal/core/domain"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

type runtimeSnapshot struct {
	pid              int
	healthStatus     string
	lastHeartbeat    time.Time
	failureCount     int
	circuitOpenUntil time.Time
	protocolVersion  string
}

// CreateInstance 在数据库中创建插件实例配置。
// 包括校验业务名称唯一性、分配实例 ID 和端口，并写入数据库。
func (lm *LifecycleManager) CreateInstance(displayName, pluginID, version, bizName string) (string, error) {
	return lm.CreateInstanceWithPlacement("", 0, displayName, pluginID, version, bizName)
}

// CreateInstanceWithPlacement 支持在共识流程中复用同一实例 ID 与端口。
// 当 instanceID 或 port 为零值时，函数会自动分配；否则使用显式传入的值以保证多节点一致。
func (lm *LifecycleManager) CreateInstanceWithPlacement(instanceID string, port int, displayName, pluginID, version, bizName string) (string, error) {
	var count int
	if err := lm.db.QueryRow("SELECT COUNT(*) FROM plugin_instances WHERE biz_name = ?", bizName).Scan(&count); err != nil {
		return "", fmt.Errorf("检查 biz_name 时数据库出错: %w", err)
	}
	if count > 0 {
		return "", fmt.Errorf("业务组名称 (biz_name) '%s' 已被其他插件实例占用", bizName)
	}

	if instanceID == "" {
		instanceID = uuid.New().String()
	}
	if port == 0 {
		freePort, err := findFreePort()
		if err != nil {
			return "", fmt.Errorf("寻找可用端口失败: %w", err)
		}
		port = freePort
	}

	const insert = `
        INSERT INTO plugin_instances
            (instance_id, display_name, plugin_id, version, biz_name, port)
        VALUES (?, ?, ?, ?, ?, ?)
    `
	if _, err := lm.db.Exec(insert, instanceID, displayName, pluginID, version, bizName, port); err != nil {
		return "", fmt.Errorf("创建插件实例配置失败: %w", err)
	}

	log.Printf("✅ [LifecycleManager] 已成功创建插件实例 '%s' (ID: %s)，绑定到业务组 '%s'，端口 %d。", displayName, instanceID, bizName, port)
	return instanceID, nil
}

// AllocateInstancePlacement 为实例创建提前分配唯一 ID 与端口，用于共识复制前保持幂等性。
func (lm *LifecycleManager) AllocateInstancePlacement() (string, int, error) {
	id := uuid.New().String()
	port, err := findFreePort()
	if err != nil {
		return "", 0, err
	}
	return id, port, nil
}

// ListInstances 查询数据库中所有插件实例记录，并结合当前运行时快照调整其状态。
func (lm *LifecycleManager) ListInstances() (_ []domain.PluginInstance, retErr error) {
	const q = `SELECT instance_id, display_name, plugin_id, version,
                      biz_name, port, status, enabled, created_at, last_started_at
                 FROM plugin_instances`
	rows, err := lm.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("查询插件实例列表失败: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("关闭 rows 失败: %w", cerr)
		}
	}()

	runningSet := lm.snapshotRunningInstanceIDs()
	runtimeStates := lm.snapshotRuntimeStates()
	var instances []domain.PluginInstance
	for rows.Next() {
		var inst domain.PluginInstance
		if err := rows.Scan(
			&inst.InstanceID, &inst.DisplayName, &inst.PluginID, &inst.Version,
			&inst.BizName, &inst.Port, &inst.Status, &inst.Enabled,
			&inst.CreatedAt, &inst.LastStartedAt,
		); err != nil {
			log.Printf("⚠️ [LifecycleManager] 扫描插件实例行失败，已跳过: %v", err)
			continue
		}
		lm.reconcileStatus(&inst, runningSet)
		if snap, ok := runtimeStates[inst.InstanceID]; ok {
			lm.enrichRuntimeInfo(&inst, snap)
		}
		instances = append(instances, inst)
	}

	if errIter := rows.Err(); errIter != nil {
		return nil, errIter
	}

	return instances, nil
}

// DeleteInstance 删除数据库中指定插件实例配置。
// 仅在插件实例未运行时允许删除。
func (lm *LifecycleManager) DeleteInstance(instanceID string) error {
	lm.runningInstancesMu.Lock()
	if state, exists := lm.runningInstances[instanceID]; exists {
		if state.cmd != nil {
			lm.runningInstancesMu.Unlock()
			return fmt.Errorf("无法删除正在运行的插件实例 '%s'，请先停止它", instanceID)
		}
		delete(lm.runningInstances, instanceID)
	}
	lm.runningInstancesMu.Unlock()

	res, err := lm.db.Exec("DELETE FROM plugin_instances WHERE instance_id = ?", instanceID)
	if err != nil {
		return fmt.Errorf("从数据库删除实例 '%s' 失败: %w", instanceID, err)
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("未找到要删除的插件实例 '%s'", instanceID)
	}

	log.Printf("🗑️ [LifecycleManager] 已成功删除插件实例 '%s' 的配置。", instanceID)
	return nil
}

// snapshotRunningInstanceIDs 返回当前所有运行中插件实例的 ID 快照。
// 用于状态校准操作中减少锁竞争。
func (lm *LifecycleManager) snapshotRunningInstanceIDs() map[string]struct{} {
	lm.runningInstancesMu.RLock()
	defer lm.runningInstancesMu.RUnlock()

	clone := make(map[string]struct{}, len(lm.runningInstances))
	for id, state := range lm.runningInstances {
		if state != nil && state.cmd != nil {
			clone[id] = struct{}{}
		}
	}
	return clone
}

// reconcileStatus 根据运行快照修正插件实例的状态。
// 如果数据库记录为 RUNNING 但实际未运行，将自动修正为 STOPPED。
func (lm *LifecycleManager) reconcileStatus(inst *domain.PluginInstance, running map[string]struct{}) {
	if _, ok := running[inst.InstanceID]; ok {
		inst.Status = StatusRunning
		return
	}

	if inst.Status == StatusRunning {
		log.Printf("🔄 [LifecycleManager] 发现实例 '%s' 状态不一致 (DB: RUNNING, Actual: STOPPED)，正在自动修正...", inst.InstanceID)
		inst.Status = StatusStopped
		if _, err := lm.db.Exec(
			`UPDATE plugin_instances SET status = ? WHERE instance_id = ?`,
			StatusStopped, inst.InstanceID,
		); err != nil {
			log.Printf("⚠️ [LifecycleManager] 插件实例状态修正失败 (实例: %s): %v", inst.InstanceID, err)
		}
	}
}

func (lm *LifecycleManager) snapshotRuntimeStates() map[string]runtimeSnapshot {
	lm.runningInstancesMu.RLock()
	defer lm.runningInstancesMu.RUnlock()

	snapshot := make(map[string]runtimeSnapshot, len(lm.runningInstances))
	for id, state := range lm.runningInstances {
		if state == nil {
			continue
		}
		health := state.healthStatus
		if health == "" {
			health = HealthStatusUnreachable
		}
		snapshot[id] = runtimeSnapshot{
			pid:              state.pid,
			healthStatus:     health,
			lastHeartbeat:    state.lastHeartbeat,
			failureCount:     state.failureCount,
			circuitOpenUntil: state.circuitOpenUntil,
			protocolVersion:  state.protocolVersion,
		}
	}
	return snapshot
}

func (lm *LifecycleManager) enrichRuntimeInfo(inst *domain.PluginInstance, snap runtimeSnapshot) {
	if snap.pid > 0 {
		inst.RuntimePID = intPtr(snap.pid)
	}
	inst.HealthStatus = snap.healthStatus
	if !snap.lastHeartbeat.IsZero() {
		inst.LastHeartbeat = timePtr(snap.lastHeartbeat)
	}
	if snap.failureCount > 0 {
		inst.FailureCount = intPtr(snap.failureCount)
	}
	if !snap.circuitOpenUntil.IsZero() {
		inst.CircuitOpenUntil = timePtr(snap.circuitOpenUntil)
		inst.CircuitBreakerOpen = snap.circuitOpenUntil.After(time.Now())
	} else {
		inst.CircuitBreakerOpen = false
	}
	inst.ProtocolVersion = snap.protocolVersion
}

func intPtr(v int) *int {
	val := v
	return &val
}

func timePtr(t time.Time) *time.Time {
	val := t
	return &val
}
