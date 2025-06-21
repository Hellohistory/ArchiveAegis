// Package plugin_manager file: internal/service/plugin_repository.go
package plugin_manager

import (
	"ArchiveAegis/internal/core/domain"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
)

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

// fetchRepository 从远程插件仓库源中读取原始内容
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

	const maxRepoSize = 10 << 20 // 10MB
	limited := io.LimitReader(reader, maxRepoSize)

	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("读取仓库内容失败 (URL: %s): %w", repoURL, err)
	}

	return data, nil
}

// getSourceReader 根据 URL scheme 选择合适的下载器
func (pm *PluginManager) getSourceReader(rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		absPath := filepath.Join(pm.rootDir, rawURL)
		return os.Open(absPath)
	}

	for _, d := range pm.downloaders {
		if d.SupportsScheme(u.Scheme) {
			return d.Download(u)
		}
	}

	return nil, fmt.Errorf("没有找到支持协议 '%s' 的下载器", u.Scheme)
}
