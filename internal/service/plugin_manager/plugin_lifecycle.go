// Package plugin_manager file: internal/service/plugin_lifecycle.go
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
	"time"

	"github.com/google/uuid"
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
	// ---- 1. 查询数据库 ----
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
	const (
		statusRunning = "RUNNING"
		statusStopped = "STOPPED"
	)

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

	var targetVersion *domain.PluginVersion
	for i := range manifest.Versions {
		if manifest.Versions[i].VersionString == inst.Version {
			targetVersion = &manifest.Versions[i]
			break
		}
	}
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
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动插件进程失败: %w", err)
	}

	pm.runningPluginsMu.Lock()
	pm.runningPlugins[instanceID] = cmd
	pm.runningPluginsMu.Unlock()
	log.Printf("🚀 [PluginManager] 插件实例 '%s' (%s) 进程已启动 (PID: %d)", inst.DisplayName, instanceID, cmd.Process.Pid)

	go func() {
		if _, err := pm.db.Exec("UPDATE plugin_instances SET status = 'RUNNING', last_started_at = ? WHERE instance_id = ?", time.Now(), instanceID); err != nil {
			log.Printf("⚠️ [PluginManager] 更新插件实例 '%s' 状态到 RUNNING 失败: %v", instanceID, err)
		}
	}()

	go pm.registerAndMonitorPlugin(cmd, instanceID, "localhost:"+strconv.Itoa(inst.Port), inst.BizName)
	return nil
}

// Stop 停止一个正在运行的插件实例。
func (pm *PluginManager) Stop(instanceID string) error {
	pm.runningPluginsMu.Lock()
	defer pm.runningPluginsMu.Unlock()

	cmd, isRunning := pm.runningPlugins[instanceID]
	if !isRunning {
		_, _ = pm.db.Exec("UPDATE plugin_instances SET status = 'STOPPED' WHERE instance_id = ?", instanceID)
		return fmt.Errorf("插件实例 '%s' 并未在运行中", instanceID)
	}

	if err := cmd.Process.Kill(); err != nil {
		log.Printf("⚠️ [PluginManager] 停止插件进程 (PID: %d) 失败: %v", cmd.Process.Pid, err)
	}
	delete(pm.runningPlugins, instanceID)

	pm.registryMu.Lock()
	var bizToUnregister string
	for biz, iID := range pm.bizToInstanceID {
		if iID == instanceID {
			bizToUnregister = biz
			break
		}
	}
	if bizToUnregister != "" {
		delete(pm.dataSourceRegistry, bizToUnregister)
		delete(pm.bizToInstanceID, bizToUnregister)
		log.Printf("🔌 [PluginManager] 业务组 '%s' 已从网关注销。", bizToUnregister)
	}
	pm.registryMu.Unlock()

	log.Printf("👋 [PluginManager] 插件实例 '%s' 已停止。", instanceID)
	_, err := pm.db.Exec("UPDATE plugin_instances SET status = 'STOPPED' WHERE instance_id = ?", instanceID)
	return err
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
	if len(pm.dataSourceRegistry) == 0 {
		pm.registryMu.RUnlock()
		return // 没有正在运行的插件，直接返回
	}

	// 创建一个当前注册表的快照进行检查，避免长时间锁定
	registrySnapshot := make(map[string]port.DataSource)
	for bizName, ds := range pm.dataSourceRegistry {
		registrySnapshot[bizName] = ds
	}
	pm.registryMu.RUnlock()

	log.Printf("🩺 [PluginManager] 开始对 %d 个正在运行的插件实例进行健康巡检...", len(registrySnapshot))

	for bizName, dataSource := range registrySnapshot {
		go pm.checkPluginHealth(bizName, dataSource) // 并发检查每个插件
	}
}

// checkPluginHealth 负责检查单个插件的健康状况并处理结果
func (pm *PluginManager) checkPluginHealth(bizName string, ds port.DataSource) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // 设置5秒超时
	defer cancel()

	if err := ds.HealthCheck(ctx); err != nil {
		// 健康检查失败！
		log.Printf("🚨 [PluginManager] 检测到插件实例 (业务: %s) 健康检查失败: %v", bizName, err)

		pm.registryMu.RLock()
		instanceID, ok := pm.bizToInstanceID[bizName]
		pm.registryMu.RUnlock()

		if !ok {
			log.Printf("⚠️ [PluginManager] 无法找到业务 '%s' 对应的实例ID，无法处理不健康的插件。", bizName)
			return
		}

		// 将数据库中的状态更新为 ERROR
		_, dbErr := pm.db.Exec("UPDATE plugin_instances SET status = 'ERROR' WHERE instance_id = ?", instanceID)
		if dbErr != nil {
			log.Printf("⚠️ [PluginManager] 更新不健康插件 '%s' 状态到 ERROR 失败: %v", instanceID, dbErr)
		}

		// 采取断然措施：直接停止并清理这个有问题的插件进程
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

	pm.dataSourceRegistry[bizName] = adapter
	pm.bizToInstanceID[bizName] = instanceID
	*pm.closableAdapters = append(*pm.closableAdapters, adapter)

	log.Printf("✅ [PluginManager] 插件 '%s' 注册完成，业务组 '%s' 正在使用。", instanceID, bizName)
}

// monitorPlugin 监控插件进程，检测退出后做清理。
func (pm *PluginManager) monitorPlugin(cmd *exec.Cmd, instanceID string) {
	err := cmd.Wait()
	log.Printf("🔌 [PluginManager] 插件 '%s' 进程已退出，错误: %v", instanceID, err)

	// 判断：只有当前状态为 RUNNING 才调用 Stop()，防止状态覆盖
	var status string
	_ = pm.db.QueryRow("SELECT status FROM plugin_instances WHERE instance_id = ?", instanceID).Scan(&status)
	if status == "RUNNING" {
		_ = pm.Stop(instanceID)
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
