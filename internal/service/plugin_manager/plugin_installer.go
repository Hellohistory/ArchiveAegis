// Package plugin_manager file: internal/service/plugin_installer.go
package plugin_manager

import (
	"ArchiveAegis/internal/core/domain"
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Install 下载、校验并解压指定 ID 和版本的插件。
func (pm *PluginManager) Install(pluginID, version string) (err error) {
	pm.catalogMu.RLock()
	manifest, exists := pm.catalog[pluginID]
	pm.catalogMu.RUnlock()
	if !exists {
		return fmt.Errorf("插件 '%s' 不在可用插件目录中", pluginID)
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

	tempZipPath := filepath.Join(pm.installDir, fmt.Sprintf("%s-%s.tmp.zip", pluginID, version))
	defer func() {
		if err := os.Remove(tempZipPath); err != nil && !os.IsNotExist(err) {
			log.Printf("警告: 删除临时文件失败 (%s): %v", tempZipPath, err)
		}
	}()

	if err = pm.performDownload(targetVersion.Source.URL, tempZipPath); err != nil {
		return fmt.Errorf("下载插件 '%s' v%s 失败: %w", pluginID, version, err)
	}

	if targetVersion.Source.Checksum != "" {
		if err = pm.verifyChecksum(tempZipPath, targetVersion.Source.Checksum); err != nil {
			return fmt.Errorf("插件 '%s' v%s 校验失败: %w", pluginID, version, err)
		}
	}

	pluginInstallPath := filepath.Join(pm.installDir, pluginID, version)
	if err = os.RemoveAll(pluginInstallPath); err != nil {
		return fmt.Errorf("清理旧安装目录失败 (%s): %w", pluginInstallPath, err)
	}

	if err = unzip(tempZipPath, pluginInstallPath); err != nil {
		return fmt.Errorf("解压插件失败 (%s): %w", pluginID, err)
	}

	query := `
        INSERT INTO installed_plugins (plugin_id, version, install_path)
        VALUES (?, ?, ?)
        ON CONFLICT(plugin_id, version) DO UPDATE SET install_path = excluded.install_path
    `
	if _, err = pm.db.Exec(query, pluginID, version, pluginInstallPath); err != nil {
		return fmt.Errorf("更新插件安装记录失败 (插件: %s, 版本: %s): %w", pluginID, version, err)
	}

	log.Printf("🎉 [PluginManager] 插件 '%s' v%s 安装成功，路径: %s", pluginID, version, pluginInstallPath)
	return nil
}

// performDownload 执行下载操作
func (pm *PluginManager) performDownload(sourceURL, destPath string) error {
	reader, err := pm.getSourceReader(sourceURL)
	if err != nil {
		return fmt.Errorf("获取源读取器失败 (URL: %s): %w", sourceURL, err)
	}
	defer reader.Close()

	outFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("创建目标文件失败 (路径: %s): %w", destPath, err)
	}
	defer outFile.Close()

	written, err := io.Copy(outFile, reader)
	if err != nil {
		return fmt.Errorf("下载写入失败 (源: %s, 目标: %s): %w", sourceURL, destPath, err)
	}

	log.Printf("信息: 下载完成，源: %s，目标: %s，共写入 %d 字节", sourceURL, destPath, written)
	return nil
}

// verifyChecksum 校验文件的哈希值
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

// unzip 解压 zip 文件
func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("打开 zip 文件失败 (%s): %w", src, err)
	}
	defer r.Close()

	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("创建解压目录失败 (%s): %w", dest, err)
	}

	for _, f := range r.File {
		cleanName := filepath.Clean(f.Name)
		fpath := filepath.Join(dest, cleanName)

		if relPath, err := filepath.Rel(dest, fpath); err != nil || strings.HasPrefix(relPath, "..") {
			return fmt.Errorf("检测到潜在非法路径 (文件: %s)", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fpath, 0755); err != nil {
				return fmt.Errorf("创建目录失败 (%s): %w", fpath, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return fmt.Errorf("创建文件父目录失败 (%s): %w", fpath, err)
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fallbackMode(f.Mode()))
		if err != nil {
			return fmt.Errorf("创建文件失败 (%s): %w", fpath, err)
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return fmt.Errorf("打开 zip 内部文件失败 (%s): %w", f.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return fmt.Errorf("写入文件失败 (%s): %w", fpath, err)
		}
	}
	return nil
}

// fallbackMode 用于处理 zip 中 mode 缺失的场景
func fallbackMode(m os.FileMode) os.FileMode {
	if m == 0 {
		return 0644
	}
	return m
}
