// Package plugin_manager file：internal/service/plugin_manager/plugin_lifecycle.go
package plugin_manager

import (
	"ArchiveAegis/internal/adapter/datasource/grpc_client"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	statusRunning = "RUNNING"
	statusStopped = "STOPPED"
	statusError   = "ERROR"
)

// CreateInstance 在数据库中创建插件实例的配置。
func (pm *PluginManager) CreateInstance(
	displayName, pluginID, version, bizName string,
) (string, error) {

	var count int
	if err := pm.db.
		QueryRow("SELECT COUNT(*) FROM plugin_instances WHERE biz_name = ?", bizName).
		Scan(&count); err != nil {
		return "", fmt.Errorf("检查 biz_name 时数据库出错: %w", err)
	}
	if count > 0 {
		return "", fmt.Errorf("业务组名称 (biz_name) '%s' 已被其他插件实例占用", bizName)
	}

	freePort, err := findFreePort()
	if err != nil {
		return "", fmt.Errorf("寻找可用端口失败: %w", err)
	}

	instanceID := uuid.New().String()
	const insert = `
        INSERT INTO plugin_instances
            (instance_id, display_name, plugin_id, version, biz_name, port)
        VALUES (?, ?, ?, ?, ?, ?)
    `
	if _, err := pm.db.Exec(
		insert, instanceID, displayName, pluginID, version, bizName, freePort,
	); err != nil {
		return "", fmt.Errorf("创建插件实例配置失败: %w", err)
	}

	log.Printf(
		"✅ [PluginManager] 已成功创建插件实例 '%s' (ID: %s)，绑定到业务组 '%s'。",
		displayName, instanceID, bizName,
	)
	return instanceID, nil
}

// ListInstances 查询所有插件实例并校准状态。
// 返回值 error 将综合 rows.Err() 与 rows.Close() 的错误。
func (pm *PluginManager) ListInstances() (_ []domain.PluginInstance, retErr error) {
	// 查询数据库
	const q = `SELECT instance_id, display_name, plugin_id, version,
	                  biz_name, port, status, enabled, created_at, last_started_at
	             FROM plugin_instances`
	rows, err := pm.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("查询插件实例列表失败: %w", err)
	}

	defer func() {
		if cerr := rows.Close(); cerr != nil && retErr == nil { // 若主流程未出错
			retErr = fmt.Errorf("关闭 rows 失败: %w", cerr)
		}
	}()

	runningSet := pm.snapshotRunning()

	var instances []domain.PluginInstance
	for rows.Next() {
		var inst domain.PluginInstance
		if err := rows.Scan(
			&inst.InstanceID, &inst.DisplayName, &inst.PluginID, &inst.Version,
			&inst.BizName, &inst.Port, &inst.Status, &inst.Enabled,
			&inst.CreatedAt, &inst.LastStartedAt,
		); err != nil {
			log.Printf("⚠️ [PluginManager] 扫描插件实例行失败，已跳过: %v", err)
			continue
		}
		pm.reconcileStatus(&inst, runningSet)
		instances = append(instances, inst)
	}

	if errIter := rows.Err(); errIter != nil {
		return nil, errIter // retErr 仍为 nil，defer 可继续覆盖
	}

	return instances, nil
}

/*
snapshotRunning 拷贝一份当前正在运行实例的集合，避免在主循环内反复加锁。
集合类型用 map[string]struct{}，查找 O(1)。
*/
func (pm *PluginManager) snapshotRunning() map[string]struct{} {
	pm.runningPluginsMu.Lock()
	defer pm.runningPluginsMu.Unlock()

	clone := make(map[string]struct{}, len(pm.runningPlugins))
	for id := range pm.runningPlugins {
		clone[id] = struct{}{}
	}
	return clone
}

/*
reconcileStatus 根据运行快照修正单条实例的状态，并在必要时回写数据库。
*/
func (pm *PluginManager) reconcileStatus(
	inst *domain.PluginInstance, running map[string]struct{},
) {
	if _, ok := running[inst.InstanceID]; ok {
		inst.Status = statusRunning
		return
	}

	// 数据库存的是 RUNNING，但实际上进程不在 → 自动修正为 STOPPED
	if inst.Status == statusRunning {
		inst.Status = statusStopped
		if _, err := pm.db.Exec(
			`UPDATE plugin_instances SET status = ? WHERE instance_id = ?`,
			statusStopped, inst.InstanceID,
		); err != nil {
			log.Printf("⚠️ [PluginManager] 插件实例状态修正失败 (实例: %s): %v",
				inst.InstanceID, err)
		}
	}
}

// DeleteInstance 从数据库中删除一个插件实例的配置。
func (pm *PluginManager) DeleteInstance(instanceID string) error {
	pm.runningPluginsMu.Lock()
	_, isRunning := pm.runningPlugins[instanceID]
	pm.runningPluginsMu.Unlock()
	if isRunning {
		return fmt.Errorf("无法删除正在运行的插件实例 '%s'，请先停止它", instanceID)
	}

	res, err := pm.db.Exec("DELETE FROM plugin_instances WHERE instance_id = ?", instanceID)
	if err != nil {
		return fmt.Errorf("从数据库删除实例 '%s' 失败: %w", instanceID, err)
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("未找到要删除的插件实例 '%s'", instanceID)
	}

	log.Printf("🗑️ [PluginManager] 已成功删除插件实例 '%s' 的配置。", instanceID)
	return nil
}

// Start 启动一个已配置的插件实例。
func (pm *PluginManager) Start(instanceID string) error {
	pm.runningPluginsMu.Lock()
	if _, isRunning := pm.runningPlugins[instanceID]; isRunning {
		pm.runningPluginsMu.Unlock()
		return fmt.Errorf("插件实例 '%s' 已经在运行中", instanceID)
	}
	pm.runningPluginsMu.Unlock()

	var inst domain.PluginInstance
	var installPath string
	query := `SELECT pi.display_name, pi.plugin_id, pi.version, pi.biz_name, pi.port, ip.install_path 
              FROM plugin_instances pi 
              JOIN installed_plugins ip ON pi.plugin_id = ip.plugin_id AND pi.version = ip.version
              WHERE pi.instance_id = ?`
	if err := pm.db.QueryRow(query, instanceID).Scan(&inst.DisplayName, &inst.PluginID, &inst.Version, &inst.BizName, &inst.Port, &installPath); err != nil {
		return fmt.Errorf("未找到插件实例 '%s' 或其安装信息: %w", instanceID, err)
	}

	pm.catalogMu.RLock()
	manifest, ok := pm.catalog[inst.PluginID]
	pm.catalogMu.RUnlock()
	if !ok {
		return fmt.Errorf("插件 '%s' 的清单信息未在目录中找到", inst.PluginID)
	}

	targetVersion := findVersion(manifest.Versions, inst.Version)
	if targetVersion == nil {
		return fmt.Errorf("插件 '%s' 的已安装版本 '%s' 的清单信息未找到", inst.PluginID, inst.Version)
	}

	cmdPath := filepath.Join(installPath, targetVersion.Execution.Entrypoint)
	instanceDir, err := filepath.Abs(filepath.Dir(pm.installDir))
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
	// 将日志重定向到独立文件，而不是标准输出
	logPath, err := pm.getInstanceAssetPath(instanceID, "log")
	if err != nil {
		return fmt.Errorf("无法获取日志文件路径: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("无法打开插件日志文件 '%s': %w", logPath, err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动插件进程失败: %w", err)
	}

	// 进程启动成功后，立即写入PID文件
	pidPath, err := pm.getInstanceAssetPath(instanceID, "pid")
	if err != nil {
		log.Printf("⚠️ [PluginManager] 无法获取PID文件路径，将跳过写入: %v", err)
	} else {
		if err := writePIDFile(pidPath, cmd.Process.Pid); err != nil {
			log.Printf("⚠️ [PluginManager] 写入 PID 文件 '%s' 失败: %v。强烈建议手动停止实例以避免孤儿进程。", pidPath, err)
		}
	}

	pm.runningPluginsMu.Lock()
	pm.runningPlugins[instanceID] = cmd
	pm.runningPluginsMu.Unlock()
	// 更新日志消息以包含日志文件路径
	log.Printf("🚀 [PluginManager] 插件实例 '%s' (%s) 进程已启动 (PID: %d)，日志位于: %s", inst.DisplayName, instanceID, cmd.Process.Pid, logPath)

	go func() {
		if _, err := pm.db.Exec(
			"UPDATE plugin_instances SET status = ?, last_started_at = ? WHERE instance_id = ?",
			statusRunning, time.Now(), instanceID); err != nil {
			log.Printf("⚠️ [PluginManager] 更新插件实例 '%s' 状态到 RUNNING 失败: %v", instanceID, err)
		}
	}()

	go pm.registerAndMonitorPlugin(cmd, instanceID, "localhost:"+strconv.Itoa(inst.Port), inst.BizName)
	return nil
}

// Stop 停止一个正在运行的插件实例。
func (pm *PluginManager) Stop(instanceID string) error {
	pm.runningPluginsMu.Lock()
	cmd, isRunning := pm.runningPlugins[instanceID]
	if !isRunning {
		pm.runningPluginsMu.Unlock()
		// 即使不在运行，也确保数据库状态正确
		_, _ = pm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", statusStopped, instanceID)
		return fmt.Errorf("插件实例 '%s' 并未在运行中", instanceID)
	}
	pm.runningPluginsMu.Unlock() // 尽早释放锁

	log.Printf("ℹ️ [PluginManager] 正在尝试优雅地停止插件实例 '%s' (PID: %d)...", instanceID, cmd.Process.Pid)

	// 发送 SIGTERM 信号，请求插件优雅退出
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("⚠️ [PluginManager] 发送 SIGTERM 信号到插件 '%s' 失败: %v。将尝试强制杀死。", instanceID, err)
		return pm.forceKill(instanceID, cmd) // 封装一个强制杀死的方法
	}

	// 等待插件进程自己退出，但设置一个超时
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// 进程正常退出
		log.Printf("✅ [PluginManager] 插件 '%s' 已成功优雅退出。", instanceID)
		pm.cleanupAfterStop(instanceID)
		if err != nil {
			// 如果 wait 返回错误，记录它
			log.Printf("ℹ️ [PluginManager] 插件 '%s' 退出时返回错误: %v", instanceID, err)
		}
		return nil
	case <-time.After(10 * time.Second): // 10秒超时
		// 进程没有在规定时间内退出，强制杀死
		log.Printf("⚠️ [PluginManager] 插件 '%s' 在10秒内未响应 SIGTERM，将执行强制杀死。", instanceID)
		return pm.forceKill(instanceID, cmd)
	}
}

// forceKill 辅助函数：强制杀死并清理
func (pm *PluginManager) forceKill(instanceID string, cmd *exec.Cmd) error {
	if err := cmd.Process.Kill(); err != nil {
		log.Printf("🚨 [PluginManager] 强制杀死插件进程 (PID: %d) 失败: %v", cmd.Process.Pid, err)
	}
	pm.cleanupAfterStop(instanceID)
	return fmt.Errorf("插件 '%s' 被强制杀死", instanceID)
}

// cleanupAfterStop 辅助函数：统一处理停止后的清理工作
func (pm *PluginManager) cleanupAfterStop(instanceID string) {
	pm.runningPluginsMu.Lock()
	delete(pm.runningPlugins, instanceID)
	pm.runningPluginsMu.Unlock()

	// 清理PID文件
	pidPath, err := pm.getInstanceAssetPath(instanceID, "pid")
	if err != nil {
		log.Printf("⚠️ [PluginManager] 无法获取实例 '%s' 的PID文件路径进行清理: %v", instanceID, err)
	} else {
		cleanupPIDFile(pidPath)
	}

	pm.registryMu.Lock()
	defer pm.registryMu.Unlock()

	var bizToUnregister string
	for biz, iID := range pm.bizToInstanceID {
		if iID == instanceID {
			bizToUnregister = biz
			break
		}
	}
	if bizToUnregister != "" {
		delete(pm.executorRegistry, bizToUnregister)
		delete(pm.bizToInstanceID, bizToUnregister)
		log.Printf("🔌 [PluginManager] 业务组 '%s' 已从网关注销。", bizToUnregister)
	}

	log.Printf("👋 [PluginManager] 插件实例 '%s' 已停止。", instanceID)
	_, _ = pm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", statusStopped, instanceID)
}

// StartHealthChecks 用于启动后台健康检查任务
func (pm *PluginManager) StartHealthChecks(interval time.Duration) {
	log.Printf("✅ [PluginManager] 健康检查服务已启动，巡检周期: %v", interval)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			<-ticker.C
			pm.performAllHealthChecks()
		}
	}()
}

// performAllHealthChecks 执行一轮完整的健康检查
func (pm *PluginManager) performAllHealthChecks() {
	pm.registryMu.RLock()
	if len(pm.executorRegistry) == 0 {
		pm.registryMu.RUnlock()
		return
	}

	registrySnapshot := make(map[string]port.Executor)
	for bizName, executor := range pm.executorRegistry {
		registrySnapshot[bizName] = executor
	}
	pm.registryMu.RUnlock()

	log.Printf("🩺 [PluginManager] 开始对 %d 个正在运行的插件实例进行健康巡检...", len(registrySnapshot))

	for bizName, executor := range registrySnapshot {
		go pm.checkPluginHealth(bizName, executor) // 并发检查每个插件
	}
}

// checkPluginHealth 负责检查单个插件的健康状况并处理结果
func (pm *PluginManager) checkPluginHealth(bizName string, executor port.Executor) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := executor.HealthCheck(ctx); err != nil {
		// 健康检查失败！
		log.Printf("🚨 [PluginManager] 检测到插件实例 (业务: %s) 健康检查失败: %v", bizName, err)

		pm.registryMu.RLock()
		instanceID, ok := pm.bizToInstanceID[bizName]
		pm.registryMu.RUnlock()

		if !ok {
			log.Printf("⚠️ [PluginManager] 无法找到业务 '%s' 对应的实例ID，无法处理不健康的插件。", bizName)
			return
		}

		_, dbErr := pm.db.Exec("UPDATE plugin_instances SET status = ? WHERE instance_id = ?", statusError, instanceID)
		if dbErr != nil {
			log.Printf("⚠️ [PluginManager] 更新不健康插件 '%s' 状态到 ERROR 失败: %v", instanceID, dbErr)
		}

		log.Printf("- [PluginManager] 正在停止不健康的插件实例 '%s'...", instanceID)
		if stopErr := pm.Stop(instanceID); stopErr != nil {
			log.Printf("⚠️ [PluginManager] 停止不健康插件 '%s' 时发生错误: %v", instanceID, stopErr)
		}
	}
}

// registerAndMonitorPlugin 连接到新启动的插件，将其注册到网关，并监控其生命周期。
func (pm *PluginManager) registerAndMonitorPlugin(cmd *exec.Cmd, instanceID, address, bizName string) {
	adapter, err := pm.tryConnectPlugin(address, instanceID)
	if err != nil {
		log.Printf("⚠️ [PluginManager] 插件 '%s' 无法连接: %v", instanceID, err)
		_ = pm.Stop(instanceID)
		return
	}

	pm.registerPlugin(instanceID, bizName, adapter)

	// 异步监控插件生命周期，避免阻塞主流程
	go pm.monitorPlugin(cmd, instanceID)
}

// tryConnectPlugin 尝试连接插件服务，最多重试 5 次，采用指数退避策略。
func (pm *PluginManager) tryConnectPlugin(address, instanceID string) (*grpc_client.ClientAdapter, error) {
	var adapter *grpc_client.ClientAdapter
	var err error
	baseDelay := time.Second

	for i := 0; i < 5; i++ {
		log.Printf("ℹ️ [PluginManager] 尝试连接插件 '%s' (%s)，第 %d 次...", instanceID, address, i+1)

		adapter, err = grpc_client.New(address)
		if err == nil {
			// 使用超时上下文验证服务存活
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err = adapter.GetPluginInfo(ctx)
			cancel()
			if err == nil {
				log.Printf("✅ [PluginManager] 成功连接插件 '%s'", instanceID)
				return adapter, nil
			}
		}

		// 指数退避延迟（1s, 2s, 4s, 8s, 16s）
		time.Sleep(baseDelay << i)
	}

	return nil, fmt.Errorf("连接插件 '%s' 失败（%v）", instanceID, err)
}

// registerPlugin 将插件适配器注册进插件管理器内部注册表。
func (pm *PluginManager) registerPlugin(instanceID, bizName string, adapter *grpc_client.ClientAdapter) {
	pm.registryMu.Lock()
	defer pm.registryMu.Unlock()

	pm.executorRegistry[bizName] = adapter
	pm.bizToInstanceID[bizName] = instanceID
	*pm.closableAdapters = append(*pm.closableAdapters, adapter)

	log.Printf("✅ [PluginManager] 插件 '%s' 注册完成，业务组 '%s' 正在使用。", instanceID, bizName)
}

// monitorPlugin 监控插件进程，检测退出后做清理。
func (pm *PluginManager) monitorPlugin(cmd *exec.Cmd, instanceID string) {
	err := cmd.Wait()
	log.Printf("🔌 [PluginManager] 插件 '%s' 进程已退出，错误: %v", instanceID, err)
	pm.cleanupAfterStop(instanceID)
}

// ReconcileOrphanedPlugins 在管理器启动时检查并处理孤儿进程。
func (pm *PluginManager) ReconcileOrphanedPlugins() error {
	log.Printf("🔄 [PluginManager] 开始巡检并清理潜在的孤儿插件进程...")

	// 1. 从数据库获取所有实例的ID
	rows, err := pm.db.Query(`SELECT instance_id FROM plugin_instances`)
	if err != nil {
		return fmt.Errorf("无法查询所有实例ID: %w", err)
	}
	defer rows.Close()

	var orphanedCount int
	for rows.Next() {
		var instanceID string
		if err := rows.Scan(&instanceID); err != nil {
			log.Printf("⚠️ [PluginManager] 扫描实例ID失败: %v", err)
			continue
		}

		// 2. 检查每个实例是否存在 PID 文件
		pidPath, err := pm.getInstanceAssetPath(instanceID, "pid")
		if err != nil {
			// 如果连路径都获取不到，说明实例信息有问题，跳过
			continue
		}

		pid, err := readPIDFile(pidPath)
		if os.IsNotExist(err) {
			// PID 文件不存在，正常
			continue
		}
		if err != nil {
			log.Printf("⚠️ [PluginManager] 读取 PID 文件 '%s' 失败: %v", pidPath, err)
			continue
		}

		// 3. 检查 PID 对应的进程是否存在
		process, err := os.FindProcess(pid)
		if err != nil {
			// 在类 Unix 系统上，FindProcess 总是成功，所以这个错误不关键
			log.Printf("ℹ️ [PluginManager] 查找进程 PID %d 时出错 (可忽略): %v", pid, err)
		}

		// 在类 Unix 系统上，向进程发送 signal 0 是检查其是否存在的标准方法
		// 在 Windows 上，此方法无效，但 FindProcess 本身就更可靠
		err = process.Signal(syscall.Signal(0))
		if err == nil {
			// 进程存在！这是一个孤儿进程。
			log.Printf("🚨 [PluginManager] 发现孤儿进程！实例: %s, PID: %d。正在尝试终止...", instanceID, pid)
			if killErr := process.Kill(); killErr != nil {
				log.Printf("🚨 [PluginManager] 强制杀死孤儿进程 PID %d 失败: %v", pid, killErr)
			} else {
				log.Printf("✅ [PluginManager] 孤儿进程 PID %d 已被成功终止。", pid)
				orphanedCount++
			}
		}

		// 4. 无论进程是否存在，只要PID文件存在于此阶段，都应被清理
		cleanupPIDFile(pidPath)
	}

	if orphanedCount > 0 {
		log.Printf("🎉 [PluginManager] 孤儿进程巡检完成，共清理了 %d 个进程。", orphanedCount)
	} else {
		log.Printf("✅ [PluginManager] 孤儿进程巡检完成，未发现任何孤儿进程。")
	}

	// 确保所有插件在数据库中的状态都是 STOPPED
	if _, err := pm.db.Exec(`UPDATE plugin_instances SET status = ? WHERE status != ?`, statusStopped, statusStopped); err != nil {
		return fmt.Errorf("重置所有插件状态为 STOPPED 失败: %w", err)
	}

	return rows.Err()
}

// 用于管理PID和日志文件的辅助函数

// getInstanceAssetPath 为插件实例生成特定资产（如pid, log）的路径
func (pm *PluginManager) getInstanceAssetPath(instanceID, assetName string) (string, error) {
	var pluginID, version string
	// 从数据库查询 pluginID 和 version，因为我们需要它们来构建安装路径
	query := `SELECT plugin_id, version FROM plugin_instances WHERE instance_id = ?`
	if err := pm.db.QueryRow(query, instanceID).Scan(&pluginID, &version); err != nil {
		return "", fmt.Errorf("无法找到实例 '%s' 的插件信息: %w", instanceID, err)
	}

	// e.g., /path/to/install/dir/<plugin_id>/<version>/assets/
	assetsDir := filepath.Join(pm.installDir, pluginID, version, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return "", fmt.Errorf("创建资产目录 '%s' 失败: %w", assetsDir, err)
	}

	// e.g., /path/to/install/dir/<plugin_id>/<version>/assets/<instance_id>.log
	return filepath.Join(assetsDir, instanceID+"."+assetName), nil
}

// writePIDFile 将进程ID写入指定文件
func writePIDFile(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

// readPIDFile 读取并返回PID
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// cleanupPIDFile 删除PID文件
func cleanupPIDFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("⚠️ [PluginManager] 警告: 清理 PID 文件 '%s' 失败: %v", path, err)
	}
}

// findFreePort 查找一个可用的 TCP 端口。
// 仅返回端口号，存在被他人抢占的竞态风险；保持向后兼容不做接口扩展。
func findFreePort() (port int, err error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("监听端口失败: %w", err)
	}

	defer func() {
		if cerr := l.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("关闭监听器失败: %w", cerr)
		}
	}()

	tcpAddr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("监听地址不是 *net.TCPAddr")
	}
	return tcpAddr.Port, nil
}

// findVersion 用于从版本清单中查找匹配的版本指针。
// 未找到时返回 nil。
func findVersion(versions []domain.PluginVersion, versionStr string) *domain.PluginVersion {
	for i := range versions {
		if versions[i].VersionString == versionStr {
			return &versions[i]
		}
	}
	return nil
}
