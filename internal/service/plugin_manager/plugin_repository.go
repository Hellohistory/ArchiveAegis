// Package plugin_manager 提供插件管理相关服务
// 文件位置: internal/service/plugin_repository.go
package plugin_manager

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/downloader"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
)

// RefreshRepositories 从所有已启用的插件仓库中获取插件清单，并更新插件目录缓存
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
			newCatalog[plugin.ID] = plugin
		}
		log.Printf("✅ [PluginManager] 成功处理仓库 '%s'，发现 %d 个插件。", repo.Name, len(repo.Plugins))
	}
	pm.catalogMu.Lock()
	pm.catalog = newCatalog
	pm.catalogMu.Unlock()
	log.Printf("🎉 [PluginManager] 所有仓库刷新完毕，当前目录中共有 %d 个唯一插件。", len(newCatalog))
}

// GetAvailablePlugins 返回当前插件目录中的所有插件清单，按 ID 排序
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

// fetchRepository 从远程插件仓库读取内容并返回 JSON 字节数据
func (pm *PluginManager) fetchRepository(repoURL string) ([]byte, error) {
	reader, err := pm.getSourceReader(repoURL)
	if err != nil {
		return nil, fmt.Errorf("获取仓库源失败 (URL: %s): %w", repoURL, err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			log.Printf("警告: 关闭仓库读取流失败 (URL: %s): %v", repoURL, err)
		}
	}()

	const maxRepoSize = 10 << 20 // 最大读取大小为 10MB
	limited := io.LimitReader(reader, maxRepoSize)

	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("读取仓库内容失败 (URL: %s): %w", repoURL, err)
	}

	return data, nil
}

// getSourceReader 根据传入 URL 获取插件清单的读取器
func (pm *PluginManager) getSourceReader(rawURL string) (io.ReadCloser, error) {
	//  Windows 绝对路径
	if runtime.GOOS == "windows" && isWindowsAbsPath(rawURL) {
		return os.Open(rawURL)
	}

	// 尝试解析为 URL
	u, err := url.Parse(rawURL)
	if err != nil {
		// 无法解析时按相对路径处理
		abs := filepath.Join(pm.rootDir, rawURL)
		return os.Open(abs)
	}

	// 无 scheme：处理为本地路径
	if u.Scheme == "" {
		abs := filepath.Join(pm.rootDir, u.Path)
		return os.Open(abs)
	}

	// file 协议路径
	if u.Scheme == "file" {
		localPath := downloader.ResolveLocalFilePath(u)
		return os.Open(localPath)
	}

	// 其他协议（如 http/https）
	for _, d := range pm.downloaders {
		if d.SupportsScheme(u.Scheme) {
			return d.Download(u)
		}
	}
	return nil, fmt.Errorf("没有找到支持协议 '%s' 的下载器", u.Scheme)
}

// isWindowsAbsPath 判断路径是否为 Windows 平台上的绝对路径（如 C:\ 或 D:/ 开头）
func isWindowsAbsPath(p string) bool {
	absWin := regexp.MustCompile(`^[a-zA-Z]:[\\/].+`)
	return absWin.MatchString(p)
}
