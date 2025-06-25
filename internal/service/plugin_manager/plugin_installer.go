// Package plugin_manager file: internal/service/plugin_manager/plugin_installer.go
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

	"github.com/google/uuid"
)

// Install 是安装插件或启用系统功能的主入口。
// 它会根据插件清单中的类型，分发到不同的处理流程。
func (pm *PluginManager) Install(pluginID, version string) (err error) {
	pm.catalogMu.RLock()
	manifest, exists := pm.catalog[pluginID]
	pm.catalogMu.RUnlock()
	if !exists {
		return fmt.Errorf("插件 '%s' 不在可用插件目录中", pluginID)
	}

	// 根据插件清单中的类型来决定执行路径。
	switch manifest.Type {
	case domain.PluginTypeSystemFeature:
		log.Printf("⚙️ [PluginManager] 正在启用系统功能 '%s'...", pluginID)
		return pm.enableSystemFeature(pluginID, true)

	case domain.PluginTypeStandard, "": // 将空字符串视作标准插件，以兼容旧格式。
		return pm.installStandardPlugin(manifest, pluginID, version)

	default:
		return fmt.Errorf("不支持的插件类型: '%s'", manifest.Type)
	}
}

// installStandardPlugin 处理标准插件的下载、校验、解压和注册流程。
func (pm *PluginManager) installStandardPlugin(manifest domain.PluginManifest, pluginID, version string) (err error) {
	// 查找并验证需要安装的版本是否存在。
	var targetVersion *domain.PluginVersion
	for i := range manifest.Versions {
		if manifest.Versions[i].VersionString == version {
			targetVersion = &manifest.Versions[i]
			break
		}
	}
	if targetVersion == nil {
		return fmt.Errorf("标准插件 '%s' 的版本 '%s' 未找到", pluginID, version)
	}

	log.Printf("⚙️ [PluginManager] 开始安装标准插件 '%s' v%s...", pluginID, version)

	// 下载到唯一的临时 zip 文件。
	tempZipPath := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s-%s.tmp.zip", pluginID, version, uuid.NewString()))
	// 确保在任何情况下都能清理临时下载的zip文件。
	defer func() {
		if err := os.Remove(tempZipPath); err != nil && !os.IsNotExist(err) {
			log.Printf("警告: 删除临时zip文件失败 (%s): %v", tempZipPath, err)
		}
	}()

	if err = pm.performDownload(targetVersion.Source.URL, tempZipPath); err != nil {
		return fmt.Errorf("下载插件 '%s' v%s 失败: %w", pluginID, version, err)
	}

	// 校验文件的哈希值。
	if targetVersion.Source.Checksum != "" {
		if err = pm.verifyChecksum(tempZipPath, targetVersion.Source.Checksum); err != nil {
			return fmt.Errorf("插件 '%s' v%s 校验失败: %w", pluginID, version, err)
		}
	}

	// 为保证安装过程的原子性，先解压到唯一的临时目录。
	pluginInstallPath := filepath.Join(pm.installDir, pluginID, version)
	tempUnzipPath := pluginInstallPath + ".tmp-unzip-" + uuid.NewString()

	// 确保在安装失败时，能够清理临时的解压目录。
	defer func() {
		if err != nil {
			if err := os.RemoveAll(tempUnzipPath); err != nil {
				log.Printf("警告: 清理失败的临时解压目录失败 (%s): %v", tempUnzipPath, err)
			}
		}
	}()

	if err = unzip(tempZipPath, tempUnzipPath); err != nil {
		return fmt.Errorf("解压插件失败 (%s): %w", pluginID, err)
	}

	// 先将安装记录写入数据库，这是“逻辑提交”。
	query := `
        INSERT INTO installed_plugins (plugin_id, version, install_path)
        VALUES (?, ?, ?)
        ON CONFLICT(plugin_id, version) DO UPDATE SET install_path = excluded.install_path
    `
	if _, err = pm.db.Exec(query, pluginID, version, pluginInstallPath); err != nil {
		return fmt.Errorf("更新插件安装记录失败 (插件: %s, 版本: %s): %w", pluginID, version, err)
	}

	// 清理可能存在的旧版本目录，为新版本让路。
	if err = os.RemoveAll(pluginInstallPath); err != nil {
		log.Printf("警告: 清理旧安装目录失败 (%s)，但这不应影响新版本安装: %v", pluginInstallPath, err)
	}

	// 通过原子性的重命名操作，“提交”文件系统变更。
	if err = os.Rename(tempUnzipPath, pluginInstallPath); err != nil {
		log.Printf("🚨 [PluginManager] 严重错误！插件 '%s' v%s 安装失败在最后一步: %v", pluginID, version, err)
		log.Printf("🚨 [PluginManager] 状态不一致：数据库已记录安装，但文件位于 '%s'。请手动将其重命名为 '%s'。", tempUnzipPath, pluginInstallPath)
		return fmt.Errorf("提交插件文件系统变更失败: %w", err)
	}

	log.Printf("🎉 [PluginManager] 插件 '%s' v%s 安装成功，路径: %s", pluginID, version, pluginInstallPath)
	return nil
}

// performDownload 执行下载操作。
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

// verifyChecksum 校验文件的哈希值。
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

// unzip 解压 zip 文件。
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

// extractFile 解压 zip 包中的单个文件或目录。
func extractFile(f *zip.File, dest string) error {
	cleanName := filepath.Clean(f.Name)
	fpath := filepath.Join(dest, cleanName)

	// 防止 Zip Slip 攻击（路径穿越），确保解压路径在目标目录内。
	if relPath, err := filepath.Rel(dest, fpath); err != nil || strings.HasPrefix(relPath, "..") {
		return fmt.Errorf("检测到潜在非法路径 (文件: %s)", f.Name)
	}

	// 如果是目录，则创建它。
	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(fpath, 0755); err != nil {
			return fmt.Errorf("创建目录失败 (%s): %w", fpath, err)
		}
		return nil
	}

	// 确保目标文件的父目录存在。
	if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
		return fmt.Errorf("创建文件父目录失败 (%s): %w", fpath, err)
	}

	// 创建并打开目标文件用于写入。
	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fallbackMode(f.Mode()))
	if err != nil {
		return fmt.Errorf("创建文件失败 (%s): %w", fpath, err)
	}
	defer outFile.Close()

	// 打开 zip 包内的源文件。
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("打开 zip 内部文件失败 (%s): %w", f.Name, err)
	}
	defer rc.Close()

	// 将源文件内容拷贝到目标文件。
	if _, err := io.Copy(outFile, rc); err != nil {
		return fmt.Errorf("写入文件失败 (%s): %w", fpath, err)
	}

	return nil
}

// fallbackMode 用于处理 zip 中文件模式(permission)缺失的场景。
func fallbackMode(m os.FileMode) os.FileMode {
	if m == 0 {
		return 0644
	}
	return m
}

// enableSystemFeature 在数据库中启用或禁用一个系统级功能。
func (pm *PluginManager) enableSystemFeature(featureID string, enabled bool) error {
	query := `UPDATE system_features SET enabled = ? WHERE feature_id = ?`
	res, err := pm.db.Exec(query, enabled, featureID)
	if err != nil {
		return fmt.Errorf("更新系统功能 '%s' 状态失败: %w", featureID, err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		// 如果 UPDATE 未影响任何行，说明记录可能不存在，需要 INSERT。
		insertQuery := `INSERT INTO system_features (feature_id, enabled) VALUES (?, ?)`
		_, err = pm.db.Exec(insertQuery, featureID, enabled)
		if err != nil {
			return fmt.Errorf("插入系统功能 '%s' 状态失败: %w", featureID, err)
		}
	}
	log.Printf("✅ [PluginManager] 系统功能 '%s' 状态已设置为: %t", featureID, enabled)
	return nil
}
