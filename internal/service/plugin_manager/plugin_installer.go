// Package plugin_manager 提供插件安装与系统功能启用的相关功能
// 文件位置: internal/service/plugin_installer.go
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

// Install 执行插件的下载、校验、解压及安装路径记录
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

	// 检测是否为系统功能插件
	var manifestType string
	if len(manifest.Tags) > 0 {
		for _, tag := range manifest.Tags {
			if tag == "SYSTEM_FEATURE" {
				manifestType = "SYSTEM_FEATURE"
				break
			}
		}
	}

	if manifestType == "SYSTEM_FEATURE" {
		log.Printf("⚙️ [PluginManager] 正在启用系统功能 '%s'...", pluginID)
		return pm.enableSystemFeature(pluginID, true)
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

// performDownload 执行插件的下载流程，将数据保存到指定路径
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

// verifyChecksum 校验指定文件的哈希值是否与预期匹配
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

// unzip 解压指定的 zip 文件到目标路径
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
		if err := extractFile(f, dest); err != nil {
			return err
		}
	}
	return nil
}

// extractFile 解压单个文件或目录到指定目标路径
func extractFile(f *zip.File, dest string) error {
	cleanName := filepath.Clean(f.Name)
	fpath := filepath.Join(dest, cleanName)

	if relPath, err := filepath.Rel(dest, fpath); err != nil || strings.HasPrefix(relPath, "..") {
		return fmt.Errorf("检测到潜在非法路径 (文件: %s)", f.Name)
	}

	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(fpath, 0755); err != nil {
			return fmt.Errorf("创建目录失败 (%s): %w", fpath, err)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
		return fmt.Errorf("创建文件父目录失败 (%s): %w", fpath, err)
	}

	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fallbackMode(f.Mode()))
	if err != nil {
		return fmt.Errorf("创建文件失败 (%s): %w", fpath, err)
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("打开 zip 内部文件失败 (%s): %w", f.Name, err)
	}
	defer rc.Close()

	if _, err := io.Copy(outFile, rc); err != nil {
		return fmt.Errorf("写入文件失败 (%s): %w", fpath, err)
	}

	return nil
}

// fallbackMode 返回默认文件权限模式（当 zip 文件未提供权限信息时使用）
func fallbackMode(m os.FileMode) os.FileMode {
	if m == 0 {
		return 0644
	}
	return m
}

// enableSystemFeature 启用或禁用系统功能并更新数据库记录
func (pm *PluginManager) enableSystemFeature(featureID string, enabled bool) error {
	query := `UPDATE system_features SET enabled = ? WHERE feature_id = ?`
	res, err := pm.db.Exec(query, enabled, featureID)
	if err != nil {
		return fmt.Errorf("更新系统功能 '%s' 状态失败: %w", featureID, err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		insertQuery := `INSERT INTO system_features (feature_id, enabled) VALUES (?, ?)`
		_, err = pm.db.Exec(insertQuery, featureID, enabled)
		if err != nil {
			return fmt.Errorf("插入系统功能 '%s' 状态失败: %w", featureID, err)
		}
	}
	log.Printf("✅ [PluginManager] 系统功能 '%s' 状态已设置为: %t", featureID, enabled)
	return nil
}
