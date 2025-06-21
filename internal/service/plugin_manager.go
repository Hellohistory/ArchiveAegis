// file: internal/service/plugin_manager.go
package service

import (
	"ArchiveAegis/internal/adapter/datasource/grpc_client"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// PluginManager 负责管理插件的目录、安装和生命周期
type PluginManager struct {
	db                 *sql.DB
	repositories       []RepositoryConfig
	installDir         string
	catalog            map[string]domain.PluginManifest
	catalogMu          sync.RWMutex
	httpClient         *http.Client
	runningPlugins     map[string]*exec.Cmd
	dataSourceRegistry map[string]port.DataSource
	closableAdapters   *[]io.Closer
	runningPluginsMu   sync.Mutex
	registryMu         sync.RWMutex
	bizToInstanceID    map[string]string
}

// RepositoryConfig 是在网关主配置中定义的仓库信息
type RepositoryConfig struct {
	Name    string `mapstructure:"name"`
	URL     string `mapstructure:"url"`
	Enabled bool   `mapstructure:"enabled"`
}

// NewPluginManager 创建一个新的插件管理器实例
func NewPluginManager(db *sql.DB, repos []RepositoryConfig, installDir string, registry map[string]port.DataSource, closers *[]io.Closer) (*PluginManager, error) {
	if db == nil {
		return nil, errors.New("PluginManager 需要一个有效的数据库连接")
	}
	if installDir == "" {
		return nil, fmt.Errorf("插件安装目录(installDir)不能为空")
	}
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return nil, fmt.Errorf("创建插件安装目录 '%s' 失败: %w", installDir, err)
	}
	return &PluginManager{
		db:                 db,
		repositories:       repos,
		installDir:         installDir,
		catalog:            make(map[string]domain.PluginManifest),
		httpClient:         &http.Client{Timeout: 30 * time.Second},
		runningPlugins:     make(map[string]*exec.Cmd),
		dataSourceRegistry: registry,
		closableAdapters:   closers,
		bizToInstanceID:    make(map[string]string),
	}, nil
}

// RefreshRepositories 从所有已配置的仓库中获取信息，并更新内存中的插件目录
func (pm *PluginManager) RefreshRepositories() {
	log.Println("🔄 [PluginManager] 开始刷新所有插件仓库...")
	newCatalog := make(map[string]domain.PluginManifest)

	for _, repoCfg := range pm.repositories {
		if !repoCfg.Enabled {
			log.Printf("⚪️ [PluginManager] 仓库 '%s' 已被禁用，跳过。", repoCfg.Name)
			continue
		}
		log.Printf("⬇️ [PluginManager] 正在从仓库 '%s' (%s) 获取插件列表...", repoCfg.Name, repoCfg.URL)
		repoData, err := pm.fetchRepository(repoCfg.URL)
		if err != nil {
			log.Printf("⚠️ [PluginManager] 获取仓库 '%s' 失败: %v", repoCfg.Name, err)
			continue
		}
		var repo domain.Repository
		if err := json.Unmarshal(repoData, &repo); err != nil {
			log.Printf("⚠️ [PluginManager] 解析仓库 '%s' 的 JSON 数据失败: %v", repoCfg.Name, err)
			continue
		}
		for _, plugin := range repo.Plugins {
			if _, exists := newCatalog[plugin.ID]; exists {
				log.Printf("⚠️ [PluginManager] 发现重复的插件ID '%s'，来自仓库 '%s' 的版本将被忽略。", plugin.ID, repoCfg.Name)
				continue
			}
			newCatalog[plugin.ID] = plugin
		}
		log.Printf("✅ [PluginManager] 成功处理仓库 '%s'，发现 %d 个插件。", repo.Name, len(repo.Plugins))
	}

	pm.catalogMu.Lock()
	pm.catalog = newCatalog
	pm.catalogMu.Unlock()
	log.Printf("🎉 [PluginManager] 所有仓库刷新完毕，当前目录中共有 %d 个唯一插件。", len(newCatalog))
}

// GetAvailablePlugins 返回当前插件目录中所有可用的插件清单。
func (pm *PluginManager) GetAvailablePlugins() []domain.PluginManifest {
	pm.catalogMu.RLock()
	defer pm.catalogMu.RUnlock()
	catalogSlice := make([]domain.PluginManifest, 0, len(pm.catalog))
	for _, manifest := range pm.catalog {
		catalogSlice = append(catalogSlice, manifest)
	}
	sort.Slice(catalogSlice, func(i, j int) bool {
		return catalogSlice[i].ID < catalogSlice[j].ID
	})
	return catalogSlice
}

// Install 下载、校验并解压指定ID和版本的插件。
func (pm *PluginManager) Install(pluginID, version string) error {
	pm.catalogMu.RLock()
	manifest, exists := pm.catalog[pluginID]
	pm.catalogMu.RUnlock()
	if !exists {
		return fmt.Errorf("插件 '%s' 不在可用的插件目录中", pluginID)
	}

	var targetVersion *domain.PluginVersion
	for i := range manifest.Versions {
		if manifest.Versions[i].VersionString == version {
			targetVersion = &manifest.Versions[i]
			break
		}
	}
	if targetVersion == nil {
		return fmt.Errorf("插件 '%s' 的版本 '%s' 未找到", pluginID, version)
	}

	log.Printf("⚙️ [PluginManager] 开始安装插件 '%s' v%s...", pluginID, version)

	downloadPath := filepath.Join(pm.installDir, fmt.Sprintf("%s-%s.zip", pluginID, version))
	if err := pm.downloadFile(targetVersion.Source.URL, downloadPath); err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer os.Remove(downloadPath)

	if targetVersion.Source.Checksum != "" {
		if err := pm.verifyChecksum(downloadPath, targetVersion.Source.Checksum); err != nil {
			return fmt.Errorf("文件校验失败: %w", err)
		}
	}

	pluginInstallPath := filepath.Join(pm.installDir, pluginID, version)
	if err := os.RemoveAll(pluginInstallPath); err != nil {
		return fmt.Errorf("清理旧的安装目录失败: %w", err)
	}
	if err := unzip(downloadPath, pluginInstallPath); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	query := `INSERT INTO installed_plugins (plugin_id, version, install_path) VALUES (?, ?, ?) ON CONFLICT(plugin_id, version) DO NOTHING`
	if _, err := pm.db.Exec(query, pluginID, version, pluginInstallPath); err != nil {
		return fmt.Errorf("更新数据库已安装列表失败: %w", err)
	}
	log.Printf("🎉 [PluginManager] 插件 '%s' v%s 安装成功！", pluginID, version)
	return nil
}

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
	query := `INSERT INTO plugin_instances (instance_id, display_name, plugin_id, version, biz_name, port) VALUES (?, ?, ?, ?, ?, ?)`
	_, err = pm.db.Exec(query, instanceID, displayName, pluginID, version, bizName, port)
	if err != nil {
		return "", fmt.Errorf("创建插件实例配置失败: %w", err)
	}
	log.Printf("✅ [PluginManager] 已成功创建插件实例 '%s' (ID: %s)，绑定到业务组 '%s'。", displayName, instanceID, bizName)
	return instanceID, nil
}

// ListInstances 从数据库查询所有已配置的插件实例列表。
func (pm *PluginManager) ListInstances() ([]domain.PluginInstance, error) {
	rows, err := pm.db.Query("SELECT instance_id, display_name, plugin_id, version, biz_name, port, status, enabled, created_at, last_started_at FROM plugin_instances")
	if err != nil {
		return nil, fmt.Errorf("查询插件实例列表失败: %w", err)
	}
	defer rows.Close()

	var instances []domain.PluginInstance
	for rows.Next() {
		var p domain.PluginInstance
		if err := rows.Scan(&p.InstanceID, &p.DisplayName, &p.PluginID, &p.Version, &p.BizName, &p.Port, &p.Status, &p.Enabled, &p.CreatedAt, &p.LastStartedAt); err != nil {
			log.Printf("⚠️ [PluginManager] 扫描插件实例行失败: %v", err)
			continue
		}
		pm.runningPluginsMu.Lock()
		if _, isRunning := pm.runningPlugins[p.InstanceID]; isRunning {
			p.Status = "RUNNING"
		} else if p.Status == "RUNNING" {
			p.Status = "STOPPED"
			_, _ = pm.db.Exec("UPDATE plugin_instances SET status = 'STOPPED' WHERE instance_id = ?", p.InstanceID)
		}
		pm.runningPluginsMu.Unlock()
		instances = append(instances, p)
	}
	return instances, nil
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
	query := `SELECT pi.plugin_id, pi.version, pi.biz_name, pi.port, ip.install_path 
	          FROM plugin_instances pi 
			  JOIN installed_plugins ip ON pi.plugin_id = ip.plugin_id AND pi.version = ip.version
			  WHERE pi.instance_id = ?`
	if err := pm.db.QueryRow(query, instanceID).Scan(&inst.PluginID, &inst.Version, &inst.BizName, &inst.Port, &installPath); err != nil {
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
	argsString := strings.ReplaceAll(targetVersion.Execution.Args, "<port>", strconv.Itoa(inst.Port))
	argsString = strings.ReplaceAll(argsString, "<biz_name>", inst.BizName)
	argsString = strings.ReplaceAll(argsString, "<name>", inst.DisplayName)
	args := strings.Fields(argsString)

	cmd := exec.Command(cmdPath, args...)
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
		log.Printf("⚠️ [PluginManager] 停止插件进程 (PID: %d) 失败: %w", cmd.Process.Pid, err)
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
	time.Sleep(2 * time.Second)
	adapter, err := grpc_client.New(address)
	if err != nil {
		log.Printf("⚠️ [PluginManager] 启动后无法连接到实例 '%s' (%s): %v", instanceID, address, err)
		_ = pm.Stop(instanceID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = adapter.GetPluginInfo(ctx)
	cancel()
	if err != nil {
		log.Printf("⚠️ [PluginManager] 启动后无法从实例 '%s' 获取信息: %v", instanceID, err)
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

// DeleteInstance 从数据库中删除一个插件实例的配置。
// 前提是该实例必须处于 STOPPED 状态。
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

func (pm *PluginManager) fetchRepository(repoURL string) ([]byte, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return nil, fmt.Errorf("无效的仓库URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		resp, err := pm.httpClient.Get(repoURL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP请求失败，状态码: %d", resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	case "file":
		path := strings.TrimPrefix(u.String(), "file://")
		return os.ReadFile(path)
	default:
		return nil, fmt.Errorf("不支持的仓库URL scheme: '%s'", u.Scheme)
	}
}

func (pm *PluginManager) downloadFile(fileURL, destPath string) error {
	u, err := url.Parse(fileURL)
	if err != nil {
		return fmt.Errorf("无效的下载URL: %w", err)
	}

	if u.Scheme == "file" {
		sourcePath := strings.TrimPrefix(fileURL, "file://")
		sourceFile, err := os.Open(sourcePath)
		if err != nil {
			return fmt.Errorf("无法打开本地源文件 '%s': %w", sourcePath, err)
		}
		defer sourceFile.Close()

		destFile, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("无法创建目标文件 '%s': %w", destPath, err)
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, sourceFile)
		return err
	}

	// 对于 http/https 等协议，保持原有逻辑
	resp, err := pm.httpClient.Get(fileURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载请求失败, 状态码: %d", resp.StatusCode)
	}
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func (pm *PluginManager) verifyChecksum(filePath, expectedChecksum string) error {
	parts := strings.SplitN(expectedChecksum, ":", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return fmt.Errorf("不支持的校验算法: %s (目前仅支持 'sha256')", parts[0])
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if actualChecksum != parts[1] {
		return fmt.Errorf("校验和不匹配。期望: %s, 实际: %s", parts[1], actualChecksum)
	}
	return nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

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
