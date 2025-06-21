// Package plugin_manager file: internal/service/plugin_lifecycle.go
package plugin_manager

import (
	"ArchiveAegis/internal/adapter/datasource/grpc_client"
	"ArchiveAegis/internal/core/domain"
	"context"
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
func (pm *PluginManager) CreateInstance(displayName, pluginID, version, bizName string) (string, error) {
	var count int
	if err := pm.db.QueryRow("SELECT COUNT(*) FROM plugin_instances WHERE biz_name = ?", bizName).Scan(&count); err != nil {
		return "", fmt.Errorf("检查 biz_name 时数据库出错: %w", err)
	}
	if count > 0 {
		return "", fmt.Errorf("业务组名称 (biz_name) '%s' 已被其他插件实例占用", bizName)
	}

	port, err := findFreePort()
	if err != nil {
		return "", fmt.Errorf("寻找可用端口失败: %w", err)
	}

	instanceID := uuid.New().String()
	query := `INSERT INTO plugin_instances (instance_id, display_name, plugin_id, version, biz_name, Port) VALUES (?, ?, ?, ?, ?, ?)`
	_, err = pm.db.Exec(query, instanceID, displayName, pluginID, version, bizName, port)
	if err != nil {
		return "", fmt.Errorf("创建插件实例配置失败: %w", err)
	}

	log.Printf("✅ [PluginManager] 已成功创建插件实例 '%s' (ID: %s)，绑定到业务组 '%s'。", displayName, instanceID, bizName)
	return instanceID, nil
}

// ListInstances 从数据库查询所有已配置的插件实例列表，并校准状态
func (pm *PluginManager) ListInstances() ([]domain.PluginInstance, error) {
	rows, err := pm.db.Query(`SELECT instance_id, display_name, plugin_id, version, biz_name, port, status, enabled, created_at, last_started_at FROM plugin_instances`)
	if err != nil {
		return nil, fmt.Errorf("查询插件实例列表失败: %w", err)
	}
	defer rows.Close()

	var instances []domain.PluginInstance
	for rows.Next() {
		var p domain.PluginInstance
		if err := rows.Scan(&p.InstanceID, &p.DisplayName, &p.PluginID, &p.Version, &p.BizName, &p.Port, &p.Status, &p.Enabled, &p.CreatedAt, &p.LastStartedAt); err != nil {
			log.Printf("⚠️ [PluginManager] 扫描插件实例行失败，已跳过: %v", err)
			continue
		}

		pm.runningPluginsMu.Lock()
		if _, isRunning := pm.runningPlugins[p.InstanceID]; isRunning {
			p.Status = "RUNNING"
		} else if p.Status == "RUNNING" {
			p.Status = "STOPPED"
			_, errDb := pm.db.Exec(`UPDATE plugin_instances SET status = 'STOPPED' WHERE instance_id = ?`, p.InstanceID)
			if errDb != nil {
				log.Printf("⚠️ [PluginManager] 插件实例状态修正失败 (实例: %s): %v", p.InstanceID, errDb)
			}
		}
		pm.runningPluginsMu.Unlock()
		instances = append(instances, p)
	}

	return instances, rows.Err()
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

// registerAndMonitorPlugin 连接到新启动的插件，将其注册到网关，并监控其生命周期。
func (pm *PluginManager) registerAndMonitorPlugin(cmd *exec.Cmd, instanceID, address, bizName string) {
	var adapter *grpc_client.ClientAdapter
	var err error
	maxRetries := 5
	retryDelay := 2 * time.Second

	for i := 0; i < maxRetries; i++ {
		log.Printf("ℹ️ [PluginManager] 正在尝试连接到实例 '%s' (%s), 第 %d/%d 次...", instanceID, address, i+1, maxRetries)
		adapter, err = grpc_client.New(address)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err = adapter.GetPluginInfo(ctx)
			cancel()
			if err == nil {
				log.Printf("✅ [PluginManager] 成功连接到实例 '%s'!", instanceID)
				break
			}
		}
		if i < maxRetries-1 {
			time.Sleep(retryDelay)
		}
	}
	if err != nil {
		log.Printf("⚠️ [PluginManager] 在 %d 次尝试后，仍无法连接到实例 '%s' 并获取信息: %v", maxRetries, instanceID, err)
		_ = pm.Stop(instanceID)
		return
	}

	pm.registryMu.Lock()
	pm.dataSourceRegistry[bizName] = adapter
	pm.bizToInstanceID[bizName] = instanceID
	*pm.closableAdapters = append(*pm.closableAdapters, adapter)
	pm.registryMu.Unlock()

	log.Printf("✅ [PluginManager] 实例 '%s' 现已在地址 '%s' 上运行，并为业务组 '%s' 提供服务。", instanceID, address, bizName)

	err = cmd.Wait()
	log.Printf("🔌 [PluginManager] 检测到实例 '%s' 进程已退出，错误: %v。", instanceID, err)
	_ = pm.Stop(instanceID)
}

// findFreePort 查找一个可用的 TCP 端口
func findFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
