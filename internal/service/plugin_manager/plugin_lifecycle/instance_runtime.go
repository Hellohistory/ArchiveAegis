// Package plugin_lifecycle 提供插件实例的生命周期运行管理功能
// 文件位置: internal/service/plugin_manager/plugin_lifecycle/instance_runtime.go
package plugin_lifecycle

import (
	"ArchiveAegis/internal/core/domain"
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

// Start 启动指定插件实例。
// 启动过程包括实例配置读取、命令构造、进程启动、状态记录及运行时注册。
func (lm *LifecycleManager) Start(instanceID string) (err error) {
	lm.runningInstancesMu.Lock()
	if _, isRunning := lm.runningInstances[instanceID]; isRunning {
		lm.runningInstancesMu.Unlock()
		return fmt.Errorf("插件实例 '%s' 已经在运行中", instanceID)
	}
	lm.runningInstances[instanceID] = &runningInstance{}
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
	if ri, ok := lm.runningInstances[instanceID]; ok {
		ri.cmd = cmd
		ri.bizName = inst.BizName
	} else {
		lm.runningInstancesMu.Unlock()
		log.Printf("[LifecycleManager] CRITICAL: 实例 '%s' 占位符丢失", instanceID)
		_ = cmd.Process.Kill()
		return fmt.Errorf("实例 '%s' 状态不一致", instanceID)
	}
	lm.runningInstancesMu.Unlock()

	log.Printf("[LifecycleManager] 插件实例 '%s' (%s) 已启动，PID: %d，日志路径: %s", inst.DisplayName, instanceID, cmd.Process.Pid, logPath)

	go func() {
		_, _ = lm.db.Exec(
			"UPDATE plugin_instances SET status = ?, last_started_at = ? WHERE instance_id = ?",
			StatusRunning, time.Now(), instanceID)
	}()

	go lm.registerAndMonitorPlugin(cmd, instanceID, "localhost:"+strconv.Itoa(inst.Port), inst.BizName)
	return nil
}

// Stop 停止指定插件实例。
// 尝试发送 SIGTERM 实现优雅停止，若超时则执行强制终止。
func (lm *LifecycleManager) Stop(instanceID string) error {
	lm.runningInstancesMu.RLock()
	ri, isRunning := lm.runningInstances[instanceID]
	lm.runningInstancesMu.RUnlock()

	if !isRunning || ri.cmd == nil {
		_, _ = lm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", StatusStopped, instanceID)
		return fmt.Errorf("插件实例 '%s' 并未在运行中", instanceID)
	}

	cmd := ri.cmd
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

// cleanupAfterStop 清理插件实例停止后的所有运行状态与资源。
func (lm *LifecycleManager) cleanupAfterStop(instanceID string) {
	lm.runningInstancesMu.Lock()
	ri, ok := lm.runningInstances[instanceID]
	if !ok {
		lm.runningInstancesMu.Unlock()
		return
	}
	delete(lm.runningInstances, instanceID)
	lm.runningInstancesMu.Unlock()

	pidPath, err := lm.getInstanceAssetPath(instanceID, "pid")
	if err == nil {
		cleanupPIDFile(pidPath)
	}

	if ri.bizName != "" {
		delete(lm.executorRegistry, ri.bizName)
		log.Printf("[LifecycleManager] 已注销业务组 '%s'", ri.bizName)
	}

	if ri.adapter != nil {
		ri.adapter.Close()
	}

	log.Printf("[LifecycleManager] 插件实例 '%s' 已停止", instanceID)
	_, _ = lm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", StatusStopped, instanceID)
}

// cleanupFailedStart 清理因启动失败而占用的实例状态。
func (lm *LifecycleManager) cleanupFailedStart(instanceID string) {
	lm.runningInstancesMu.Lock()
	delete(lm.runningInstances, instanceID)
	lm.runningInstancesMu.Unlock()
	log.Printf("[LifecycleManager] 已清理启动失败的实例 '%s'", instanceID)
}
