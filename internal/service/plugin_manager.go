// file: internal/service/plugin_manager.go
package service

import (
	"ArchiveAegis/internal/core/domain"
	"archive/zip" // ✅ FIX: 导入 zip 处理包
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors" // ✅ FIX: 导入 errors 包
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	mu           sync.RWMutex
	httpClient   *http.Client
}

// RepositoryConfig 是在网关主配置中定义的仓库信息
// 这个结构体定义在 service 包内部，供 NewPluginManager 使用。
type RepositoryConfig struct {
	Name    string `mapstructure:"name"`
	URL     string `mapstructure:"url"`
	Enabled bool   `mapstructure:"enabled"`
}

// NewPluginManager 创建一个新的插件管理器实例
// ✅ FIX: 修正函数签名，添加 db *sql.DB 作为第一个参数
func NewPluginManager(db *sql.DB, repos []RepositoryConfig, installDir string) (*PluginManager, error) {
	if db == nil {
		return nil, errors.New("PluginManager 需要一个有效的数据库连接")
	}
	if installDir == "" {
		return nil, fmt.Errorf("插件安装目录(installDir)不能为空")
	}

	// 确保安装目录存在
	if err := os.MkdirAll(installDir, 0755); err != nil {
		return nil, fmt.Errorf("创建插件安装目录 '%s' 失败: %w", installDir, err)
	}

	return &PluginManager{
		db:           db,
		repositories: repos,
		installDir:   installDir,
		catalog:      make(map[string]domain.PluginManifest),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
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
	pm.mu.Lock()
	pm.catalog = newCatalog
	pm.mu.Unlock()

	log.Printf("🎉 [PluginManager] 所有仓库刷新完毕，当前目录中共有 %d 个唯一插件。", len(newCatalog))
}

// Install 下载、校验并解压指定ID和版本的插件。
func (pm *PluginManager) Install(pluginID, version string) error {
	pm.mu.RLock()
	manifest, exists := pm.catalog[pluginID]
	pm.mu.RUnlock()
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

	// 1. 下载文件
	downloadPath := filepath.Join(pm.installDir, fmt.Sprintf("%s-%s.zip", pluginID, version))
	log.Printf("⬇️ 正在从 %s 下载到 %s", targetVersion.Source.URL, downloadPath)
	if err := pm.downloadFile(targetVersion.Source.URL, downloadPath); err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer os.Remove(downloadPath) // 安装完成后删除临时的zip文件

	// 2. 校验文件 (如果提供了 checksum)
	if targetVersion.Source.Checksum != "" {
		log.Println("🔑 正在校验文件完整性...")
		if err := pm.verifyChecksum(downloadPath, targetVersion.Source.Checksum); err != nil {
			return fmt.Errorf("文件校验失败: %w", err)
		}
		log.Println("✅ 文件校验成功")
	}

	// 3. 解压文件
	pluginInstallPath := filepath.Join(pm.installDir, pluginID, version)
	log.Printf("📦 正在解压文件到 %s", pluginInstallPath)
	if err := os.RemoveAll(pluginInstallPath); err != nil {
		return fmt.Errorf("清理旧的安装目录失败: %w", err)
	}
	if err := unzip(downloadPath, pluginInstallPath); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}
	log.Println("✅ 文件解压成功")

	// 4. 更新数据库中的安装状态
	query := `INSERT INTO installed_plugins (plugin_id, installed_version, install_path, status) VALUES (?, ?, ?, 'STOPPED')
              ON CONFLICT(plugin_id) DO UPDATE SET installed_version=excluded.installed_version, install_path=excluded.install_path, status='STOPPED'`
	if _, err := pm.db.Exec(query, pluginID, version, pluginInstallPath); err != nil {
		return fmt.Errorf("更新数据库安装状态失败: %w", err)
	}
	log.Printf("🎉 [PluginManager] 插件 '%s' v%s 安装成功！", pluginID, version)

	return nil
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
		// 将 file://./configs/local_repo.json 这样的路径转换为本地文件系统路径
		path := strings.TrimPrefix(u.String(), "file://")
		return os.ReadFile(path)
	default:
		return nil, fmt.Errorf("不支持的仓库URL scheme: '%s'", u.Scheme)
	}
}

// GetAvailablePlugins 返回当前插件目录中所有可用的插件清单。
// 这个方法是线程安全的。
func (pm *PluginManager) GetAvailablePlugins() []domain.PluginManifest {
	pm.mu.RLock() // 使用读锁，允许多个并发读
	defer pm.mu.RUnlock()

	// 创建一个切片来存放结果，避免直接暴露内部的 map
	catalogSlice := make([]domain.PluginManifest, 0, len(pm.catalog))
	for _, manifest := range pm.catalog {
		catalogSlice = append(catalogSlice, manifest)
	}

	// 对结果进行排序，确保每次API返回的顺序一致
	sort.Slice(catalogSlice, func(i, j int) bool {
		return catalogSlice[i].ID < catalogSlice[j].ID
	})

	return catalogSlice
}
