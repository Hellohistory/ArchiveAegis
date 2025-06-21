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
)

// PluginManager 负责管理插件的目录、安装和生命周期
type PluginManager struct {
	db           *sql.DB
	repositories []RepositoryConfig
	installDir   string
	catalog      map[string]domain.PluginManifest
	catalogMu    sync.RWMutex
	httpClient   *http.Client

	// --- 运行时状态管理 ---
	runningPlugins   map[string]*exec.Cmd // [plugin_id -> command process]
	runningPluginsMu sync.Mutex           // 保护 runningPlugins 的访问

	// --- 与网关核心的交互 ---
	dataSourceRegistry map[string]port.DataSource // 共享网关的数据源注册表
	closableAdapters   *[]io.Closer               // 共享网关的可关闭适配器列表 (使用指针)
	registryMu         sync.RWMutex               // 为保护 registry 和 closers 新增的读写锁
	bizToPluginID      map[string]string          // biz_name -> plugin_id 的映射
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
		bizToPluginID:      make(map[string]string),
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

		// 将此仓库的插件合并到总目录中
		for _, plugin := range repo.Plugins {
			if _, exists := newCatalog[plugin.ID]; exists {
				log.Printf("⚠️ [PluginManager] 发现重复的插件ID '%s'，来自仓库 '%s' 的版本将被忽略。", plugin.ID, repoCfg.Name)
				continue
			}
			newCatalog[plugin.ID] = plugin
		}
		log.Printf("✅ [PluginManager] 成功处理仓库 '%s'，发现 %d 个插件。", repo.Name, len(repo.Plugins))
	}

	// 原子替换旧目录
	pm.catalogMu.Lock()
	pm.catalog = newCatalog
	pm.catalogMu.Unlock()

	log.Printf("🎉 [PluginManager] 所有仓库刷新完毕，当前目录中共有 %d 个唯一插件。", len(newCatalog))
}

// fetchRepository 根据 URL 的 scheme (http/file) 来获取仓库数据
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

// ListInstalled 从数据库查询所有已安装的插件列表及其状态。
func (pm *PluginManager) ListInstalled() ([]domain.InstalledPlugin, error) {
	rows, err := pm.db.Query("SELECT plugin_id, installed_version, install_path, status, installed_at, last_started_at FROM installed_plugins")
	if err != nil {
		return nil, fmt.Errorf("查询已安装插件列表失败: %w", err)
	}
	defer rows.Close()

	var installedPlugins []domain.InstalledPlugin
	for rows.Next() {
		var p domain.InstalledPlugin
		if err := rows.Scan(&p.PluginID, &p.InstalledVersion, &p.InstallPath, &p.Status, &p.InstalledAt, &p.LastStartedAt); err != nil {
			log.Printf("⚠️ [PluginManager] 扫描已安装插件行失败: %v", err)
			continue
		}

		pm.runningPluginsMu.Lock()
		if _, isRunning := pm.runningPlugins[p.PluginID]; isRunning {
			p.Status = "RUNNING"
		} else {
			if p.Status == "RUNNING" {
				p.Status = "STOPPED"
				_, _ = pm.db.Exec("UPDATE installed_plugins SET status = 'STOPPED' WHERE plugin_id = ?", p.PluginID)
			}
		}
		pm.runningPluginsMu.Unlock()

		installedPlugins = append(installedPlugins, p)
	}
	return installedPlugins, nil
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

	log.Printf("⚙️ [PluginManager] 开始安装插件 '%s' 版本 '%s'...", pluginID, version)

	downloadPath := filepath.Join(pm.installDir, fmt.Sprintf("%s-%s.zip", pluginID, version))
	log.Printf("⬇️ 正在从 %s 下载到 %s", targetVersion.Source.URL, downloadPath)
	if err := pm.downloadFile(targetVersion.Source.URL, downloadPath); err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer os.Remove(downloadPath)

	if targetVersion.Source.Checksum != "" {
		log.Println("🔑 正在校验文件完整性...")
		if err := pm.verifyChecksum(downloadPath, targetVersion.Source.Checksum); err != nil {
			return fmt.Errorf("文件校验失败: %w", err)
		}
		log.Println("✅ 文件校验成功")
	}

	pluginInstallPath := filepath.Join(pm.installDir, pluginID, version)
	log.Printf("📦 正在解压文件到 %s", pluginInstallPath)
	if err := os.RemoveAll(pluginInstallPath); err != nil {
		return fmt.Errorf("清理旧的安装目录失败: %w", err)
	}
	if err := unzip(downloadPath, pluginInstallPath); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}
	log.Println("✅ 文件解压成功")

	query := `INSERT INTO installed_plugins (plugin_id, installed_version, install_path, status) VALUES (?, ?, ?, 'STOPPED')
              ON CONFLICT(plugin_id) DO UPDATE SET installed_version=excluded.installed_version, install_path=excluded.install_path, status='STOPPED'`
	if _, err := pm.db.Exec(query, pluginID, version, pluginInstallPath); err != nil {
		return fmt.Errorf("更新数据库安装状态失败: %w", err)
	}
	log.Printf("🎉 [PluginManager] 插件 '%s' v%s 安装成功！", pluginID, version)

	return nil
}

// Start 启动一个已安装的插件。
func (pm *PluginManager) Start(pluginID string) error {
	pm.runningPluginsMu.Lock()
	if _, isRunning := pm.runningPlugins[pluginID]; isRunning {
		pm.runningPluginsMu.Unlock()
		return fmt.Errorf("插件 '%s' 已经在运行中", pluginID)
	}
	pm.runningPluginsMu.Unlock()

	var p domain.InstalledPlugin
	err := pm.db.QueryRow("SELECT installed_version, install_path FROM installed_plugins WHERE plugin_id = ?", pluginID).Scan(&p.InstalledVersion, &p.InstallPath)
	if err != nil {
		return fmt.Errorf("未找到已安装的插件 '%s' 或数据库查询失败: %w", pluginID, err)
	}

	pm.catalogMu.RLock()
	manifest, ok := pm.catalog[pluginID]
	pm.catalogMu.RUnlock()
	if !ok {
		return fmt.Errorf("插件 '%s' 的清单信息未在目录中找到", pluginID)
	}
	var targetVersion *domain.PluginVersion
	for i := range manifest.Versions {
		if manifest.Versions[i].VersionString == p.InstalledVersion {
			targetVersion = &manifest.Versions[i]
			break
		}
	}
	if targetVersion == nil {
		return fmt.Errorf("插件 '%s' 的已安装版本 '%s' 的清单信息未找到", pluginID, p.InstalledVersion)
	}

	if len(manifest.SupportedBizNames) == 0 {
		return fmt.Errorf("插件 '%s' 未在其清单中声明任何 supported_biz_names", pluginID)
	}
	bizNameForCmd := manifest.SupportedBizNames[0]

	cmdPath := filepath.Join(p.InstallPath, targetVersion.Execution.Entrypoint)
	port := pm.findFreePort()
	argsString := strings.ReplaceAll(targetVersion.Execution.Args, "<port>", strconv.Itoa(port))
	argsString = strings.ReplaceAll(argsString, "<biz_name>", bizNameForCmd)
	args := strings.Fields(argsString)

	cmd := exec.Command(cmdPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动插件进程失败: %w", err)
	}

	pm.runningPluginsMu.Lock()
	pm.runningPlugins[pluginID] = cmd
	pm.runningPluginsMu.Unlock()

	log.Printf("🚀 [PluginManager] 插件 '%s' 进程已启动 (PID: %d)", pluginID, cmd.Process.Pid)

	go func() {
		if _, err := pm.db.Exec("UPDATE installed_plugins SET status = 'RUNNING', last_started_at = ? WHERE plugin_id = ?", time.Now(), pluginID); err != nil {
			log.Printf("⚠️ [PluginManager] 更新插件 '%s' 状态到 RUNNING 失败: %v", pluginID, err)
		}
	}()

	go pm.registerAndMonitorPlugin(cmd, pluginID, "localhost:"+strconv.Itoa(port))

	return nil
}

// Stop 停止一个正在运行的插件。
func (pm *PluginManager) Stop(pluginID string) error {
	pm.runningPluginsMu.Lock()
	defer pm.runningPluginsMu.Unlock()

	cmd, isRunning := pm.runningPlugins[pluginID]
	if !isRunning {
		_, _ = pm.db.Exec("UPDATE installed_plugins SET status = 'STOPPED' WHERE plugin_id = ?", pluginID)
		return fmt.Errorf("插件 '%s' 并未在运行中", pluginID)
	}

	if err := cmd.Process.Kill(); err != nil {
		log.Printf("⚠️ [PluginManager] 停止插件进程 (PID: %d) 失败: %w", cmd.Process.Pid, err)
	}
	delete(pm.runningPlugins, pluginID)

	pm.registryMu.Lock()
	var bizToUnregister []string
	for biz, pID := range pm.bizToPluginID {
		if pID == pluginID {
			bizToUnregister = append(bizToUnregister, biz)
		}
	}
	for _, biz := range bizToUnregister {
		delete(pm.dataSourceRegistry, biz)
		delete(pm.bizToPluginID, biz)
		log.Printf("🔌 [PluginManager] 业务组 '%s' 已从网关注销。", biz)
	}
	pm.registryMu.Unlock()

	log.Printf("👋 [PluginManager] 插件 '%s' 已停止。", pluginID)

	_, err := pm.db.Exec("UPDATE installed_plugins SET status = 'STOPPED' WHERE plugin_id = ?", pluginID)
	return err
}

// --- 辅助函数 ---

func (pm *PluginManager) registerAndMonitorPlugin(cmd *exec.Cmd, pluginID, address string) {
	time.Sleep(2 * time.Second)

	adapter, err := grpc_client.New(address)
	if err != nil {
		log.Printf("⚠️ [PluginManager] 启动后无法连接到插件 '%s' (%s): %v", pluginID, address, err)
		_ = pm.Stop(pluginID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	info, err := adapter.GetPluginInfo(ctx)
	cancel()

	if err != nil {
		log.Printf("⚠️ [PluginManager] 启动后无法从插件 '%s' 获取信息: %v", pluginID, err)
		_ = pm.Stop(pluginID)
		return
	}

	pm.registryMu.Lock()
	for _, bizName := range info.SupportedBizNames {
		pm.dataSourceRegistry[bizName] = adapter
		pm.bizToPluginID[bizName] = pluginID
		log.Printf("✅ [PluginManager] 业务组 '%s' 已成功动态注册，由插件 '%s' 提供服务。", bizName, info.Name)
	}
	*pm.closableAdapters = append(*pm.closableAdapters, adapter)
	pm.registryMu.Unlock()

	err = cmd.Wait()
	log.Printf("🔌 [PluginManager] 检测到插件 '%s' 进程已退出，错误: %v。", pluginID, err)

	_ = pm.Stop(pluginID)
}

func (pm *PluginManager) downloadFile(fileURL, destPath string) error {
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

func (pm *PluginManager) findFreePort() int {
	return 50052 + (time.Now().Nanosecond() % 100)
}
