// Package plugin_lifecycle 提供插件实例的生命周期运行管理功能
// file: internal/service/plugin_manager/plugin_lifecycle/instance_runtime.go
package plugin_lifecycle

import (
	"ArchiveAegis/internal/adapter/datasource/wasm"
	"ArchiveAegis/internal/core/domain"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	ErrInstanceAlreadyRunning = errors.New("插件实例正在运行")
	ErrInstanceNotRunning     = errors.New("插件实例未运行")
	ErrIncompatibleProtocol   = errors.New("不兼容的插件协议版本")
)

const (
	gatewayProtocolMajor   = 1
	gatewayProtocolMinor   = 0
	gatewayProtocolPatch   = 0
	healthFailureThreshold = 3
	circuitOpenDuration    = 45 * time.Second
	heartbeatLossThreshold = 30 * time.Second
	maxRecoveryAttempts    = 3
	runtimeProcess         = "process"
	runtimeWasm            = "wasm"
)

// Start 启动指定插件实例。
// 启动过程包括实例配置读取、命令构造、进程启动、状态记录及运行时注册。
func (lm *LifecycleManager) Start(instanceID string) (err error) {
	lm.runningInstancesMu.Lock()
	state, exists := lm.runningInstances[instanceID]
	if !exists {
		state = &runningInstance{}
		lm.runningInstances[instanceID] = state
	}
	if state.cmd != nil || state.executor != nil {
		lm.runningInstancesMu.Unlock()
		return fmt.Errorf("%w: %s", ErrInstanceAlreadyRunning, instanceID)
	}
	state.healthStatus = HealthStatusRecovering
	state.failureCount = 0
	lm.runningInstancesMu.Unlock()

	defer func() {
		if err != nil {
			lm.cleanupFailedStart(instanceID)
		}
	}()

	var inst domain.PluginInstance
	var installPath string
	query := `SELECT pi.display_name, pi.plugin_id, pi.version, pi.biz_name, pi.port, ip.install_path
              FROM plugin_instances pi
              JOIN installed_plugins ip ON pi.plugin_id = ip.plugin_id AND pi.version = ip.version
              WHERE pi.instance_id = ?`
	if err = lm.db.QueryRow(query, instanceID).Scan(&inst.DisplayName, &inst.PluginID, &inst.Version, &inst.BizName, &inst.Port, &installPath); err != nil {
		return fmt.Errorf("未找到插件实例 '%s' 或其安装信息: %w", instanceID, err)
	}

	manifest, ok := lm.getManifest(inst.PluginID)
	if !ok {
		return fmt.Errorf("插件 '%s' 的清单信息未在目录中找到", inst.PluginID)
	}

	targetVersion := findVersion(manifest.Versions, inst.Version)
	if targetVersion == nil {
		return fmt.Errorf("插件 '%s' 的已安装版本 '%s' 的清单信息未找到", inst.PluginID, inst.Version)
	}

	runtimeKind := strings.ToLower(strings.TrimSpace(targetVersion.Execution.Runtime))
	if runtimeKind == "" {
		runtimeKind = runtimeProcess
	}

	if runtimeKind == runtimeWasm {
		return lm.startWasmInstance(instanceID, inst, targetVersion, installPath)
	}

	cmdPath := filepath.Join(installPath, targetVersion.Execution.Entrypoint)
	instanceDir, err := filepath.Abs(filepath.Dir(lm.installDir))
	if err != nil {
		return fmt.Errorf("无法确定 instance 根目录: %w", err)
	}

	replacer := strings.NewReplacer(
		"<port>", strconv.Itoa(inst.Port),
		"<biz_name>", inst.BizName,
		"<name>", inst.DisplayName,
		"<instance_dir>", instanceDir,
	)
	finalArgs := make([]string, len(targetVersion.Execution.Args))
	for i, arg := range targetVersion.Execution.Args {
		finalArgs[i] = replacer.Replace(arg)
	}

	cmd := exec.Command(cmdPath, finalArgs...)
	logPath, err := lm.getInstanceAssetPath(instanceID, "log")
	if err != nil {
		return fmt.Errorf("无法获取日志文件路径: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("无法打开插件日志文件 '%s': %w", logPath, err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err = cmd.Start(); err != nil {
		return fmt.Errorf("启动插件进程失败: %w", err)
	}

	pidPath, err := lm.getInstanceAssetPath(instanceID, "pid")
	if err == nil {
		if err = writePIDFile(pidPath, cmd.Process.Pid); err != nil {
			log.Printf("[LifecycleManager] WARNING: 写入 PID 文件失败: %v", err)
		}
	}

	lm.runningInstancesMu.Lock()
	state, ok = lm.runningInstances[instanceID]
	if !ok {
		lm.runningInstancesMu.Unlock()
		_ = cmd.Process.Kill()
		return fmt.Errorf("实例 '%s' 状态不一致", instanceID)
	}
	state.cmd = cmd
	state.bizName = inst.BizName
	state.pid = cmd.Process.Pid
	state.healthStatus = HealthStatusRecovering
	state.mode = runtimeProcess
	state.closer = nil
	lm.runningInstancesMu.Unlock()

	log.Printf("[LifecycleManager] 插件实例 '%s' (%s) 已启动，PID: %d，日志路径: %s", inst.DisplayName, instanceID, cmd.Process.Pid, logPath)

	go func() {
		_, _ = lm.db.Exec(
			"UPDATE plugin_instances SET status = ?, last_started_at = ? WHERE instance_id = ?",
			StatusRunning, time.Now(), instanceID)
	}()

	go lm.registerAndMonitorPlugin(cmd, instanceID, inst)
	return nil
}

func (lm *LifecycleManager) startWasmInstance(instanceID string, inst domain.PluginInstance, targetVersion *domain.PluginVersion, installPath string) error {
	wasmPath := filepath.Join(installPath, targetVersion.Execution.Entrypoint)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	executor, info, err := wasm.NewExecutor(ctx, wasmPath, targetVersion.Execution)
	if err != nil {
		return fmt.Errorf("启动 wasm 插件失败: %w", err)
	}

	if compatErr := ensureProtocolCompatible(info.GetContractVersion()); compatErr != nil {
		_ = executor.Close()
		return compatErr
	}

	lm.runningInstancesMu.Lock()
	state, ok := lm.runningInstances[instanceID]
	if !ok {
		lm.runningInstancesMu.Unlock()
		_ = executor.Close()
		return fmt.Errorf("实例 '%s' 状态不一致", instanceID)
	}
	state.executor = executor
	state.adapter = nil
	state.mode = runtimeWasm
	state.closer = executor
	state.healthStatus = HealthStatusHealthy
	state.failureCount = 0
	state.circuitOpenUntil = time.Time{}
	state.lastHeartbeat = time.Now()
	state.protocolVersion = formatAPIVersion(info.GetContractVersion())
	state.bizName = inst.BizName
	if inst.BizName != "" {
		lm.executorRegistry[inst.BizName] = executor
	}
	*lm.closableAdapters = append(*lm.closableAdapters, executor)
	lm.runningInstancesMu.Unlock()

	go func() {
		_, _ = lm.db.Exec(
			"UPDATE plugin_instances SET status = ?, last_started_at = ? WHERE instance_id = ?",
			StatusRunning, time.Now(), instanceID,
		)
	}()

	log.Printf("[LifecycleManager] wasm 插件实例 '%s' 已启动并注册执行器。", instanceID)
	return nil
}

// Stop 停止指定插件实例。
// 尝试发送 SIGTERM 实现优雅停止，若超时则执行强制终止。
func (lm *LifecycleManager) Stop(instanceID string) error {
	lm.runningInstancesMu.Lock()
	ri, exists := lm.runningInstances[instanceID]
	if !exists || (ri.cmd == nil && ri.executor == nil) {
		lm.runningInstancesMu.Unlock()
		_, _ = lm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", StatusStopped, instanceID)
		return ErrInstanceNotRunning
	}
	if ri.mode == runtimeWasm {
		if ri.closer != nil {
			_ = ri.closer.Close()
		}
		lm.runningInstancesMu.Unlock()
		lm.cleanupAfterStop(instanceID)
		return nil
	}
	cmd := ri.cmd
	ri.healthStatus = HealthStatusRecovering
	lm.runningInstancesMu.Unlock()

	log.Printf("[LifecycleManager] INFO: 正在优雅停止插件实例 '%s' (PID: %d)", instanceID, cmd.Process.Pid)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("[LifecycleManager] WARNING: 发送 SIGTERM 失败: %v", err)
		return lm.forceKill(instanceID, cmd)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		lm.cleanupAfterStop(instanceID)
		if err != nil {
			log.Printf("[LifecycleManager] INFO: 插件实例退出返回错误: %v", err)
		}
		return nil
	case <-time.After(10 * time.Second):
		log.Printf("[LifecycleManager] WARNING: 插件实例未在规定时间内退出，将强制终止")
		return lm.forceKill(instanceID, cmd)
	}
}

// forceKill 强制终止插件进程，并执行清理流程。
func (lm *LifecycleManager) forceKill(instanceID string, cmd *exec.Cmd) error {
	_ = cmd.Process.Kill()
	lm.cleanupAfterStop(instanceID)
	return fmt.Errorf("插件 '%s' 被强制终止", instanceID)
}

// Reload 停止并重新启动插件实例，实现热重载功能。
func (lm *LifecycleManager) Reload(instanceID string) error {
	if err := lm.Stop(instanceID); err != nil && !errors.Is(err, ErrInstanceNotRunning) {
		return err
	}
	return lm.Start(instanceID)
}

// cleanupAfterStop 清理插件实例停止后的所有运行状态与资源。
func (lm *LifecycleManager) cleanupAfterStop(instanceID string) {
	lm.runningInstancesMu.Lock()
	ri, ok := lm.runningInstances[instanceID]
	if !ok {
		lm.runningInstancesMu.Unlock()
		return
	}

	if ri.bizName != "" {
		delete(lm.executorRegistry, ri.bizName)
		log.Printf("[LifecycleManager] 已注销业务组 '%s'", ri.bizName)
	}

	if ri.adapter != nil {
		_ = ri.adapter.Close()
	}
	if ri.closer != nil && ri.mode == runtimeWasm {
		_ = ri.closer.Close()
	}

	ri.cmd = nil
	ri.executor = nil
	ri.adapter = nil
	ri.pid = 0
	ri.mode = ""
	ri.closer = nil
	ri.healthStatus = HealthStatusUnreachable
	lm.runningInstancesMu.Unlock()

	pidPath, err := lm.getInstanceAssetPath(instanceID, "pid")
	if err == nil {
		cleanupPIDFile(pidPath)
	}

	log.Printf("[LifecycleManager] 插件实例 '%s' 已停止", instanceID)
	_, _ = lm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", StatusStopped, instanceID)
}

// cleanupFailedStart 清理因启动失败而占用的实例状态。
func (lm *LifecycleManager) cleanupFailedStart(instanceID string) {
	lm.runningInstancesMu.Lock()
	if ri, ok := lm.runningInstances[instanceID]; ok {
		if ri.cmd != nil && ri.cmd.Process != nil {
			_ = ri.cmd.Process.Kill()
		}
		if ri.closer != nil {
			_ = ri.closer.Close()
		}
		ri.cmd = nil
		ri.executor = nil
		ri.adapter = nil
		ri.pid = 0
		ri.mode = ""
		ri.closer = nil
		ri.healthStatus = HealthStatusUnreachable
	}
	lm.runningInstancesMu.Unlock()
	log.Printf("[LifecycleManager] 已清理启动失败的实例 '%s'", instanceID)
}
