// Package plugin_lifecycle 提供插件实例管理中的辅助工具函数
// file: internal/service/plugin_manager/plugin_lifecycle/utils.go
package plugin_lifecycle

import (
	"ArchiveAegis/internal/adapter/datasource/grpc_client"
	"ArchiveAegis/internal/core/domain"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// tryConnectPlugin 尝试连接插件的 gRPC 服务，包含健康检查与重试机制。
func (lm *LifecycleManager) tryConnectPlugin(address, instanceID string) (*grpc_client.ClientAdapter, error) {
	var adapter *grpc_client.ClientAdapter
	var err error
	baseDelay := time.Second

	for i := 0; i < 5; i++ {
		log.Printf("ℹ️ [LifecycleManager] 尝试连接插件 '%s' (%s)，第 %d 次...", instanceID, address, i+1)

		adapter, err = grpc_client.New(address)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err = adapter.HealthCheck(ctx)
			cancel()
			if err == nil {
				log.Printf("✅ [LifecycleManager] 成功连接插件 '%s'", instanceID)
				return adapter, nil
			}
		}

		if adapter != nil {
			adapter.Close()
		}

		if i < 4 {
			time.Sleep(baseDelay << i)
		}
	}

	return nil, fmt.Errorf("连接插件 '%s' 超过最大重试次数: %w", instanceID, err)
}

// getInstanceAssetPath 构造指定插件实例的资产文件路径。
// 同时确保目录存在。
func (lm *LifecycleManager) getInstanceAssetPath(instanceID, assetName string) (string, error) {
	var pluginID, version string
	query := `SELECT plugin_id, version FROM plugin_instances WHERE instance_id = ?`
	if err := lm.db.QueryRow(query, instanceID).Scan(&pluginID, &version); err != nil {
		return "", fmt.Errorf("无法找到实例 '%s' 的插件信息: %w", instanceID, err)
	}

	assetsDir := filepath.Join(lm.installDir, pluginID, version, "assets")
	if err := os.MkdirAll(assetsDir, 0755); err != nil {
		return "", fmt.Errorf("创建资产目录 '%s' 失败: %w", assetsDir, err)
	}

	return filepath.Join(assetsDir, instanceID+"."+assetName), nil
}

// writePIDFile 将进程 ID 写入指定文件路径。
func writePIDFile(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

// readPIDFile 从文件中读取并返回进程 ID。
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// cleanupPIDFile 删除指定的 PID 文件，忽略文件不存在的情况。
func cleanupPIDFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("⚠️ [LifecycleManager] 警告: 清理 PID 文件 '%s' 失败: %v", path, err)
	}
}

// findFreePort 返回一个当前可用的本地 TCP 端口。
// 该方法不保证绝对安全，可能存在竞态条件。
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
		return 0, errors.New("监听地址不是 *net.TCPAddr 类型")
	}
	return tcpAddr.Port, nil
}

// findVersion 在版本列表中查找匹配指定版本号的插件版本。
// 若未找到，返回 nil。
func findVersion(versions []domain.PluginVersion, versionStr string) *domain.PluginVersion {
	for i := range versions {
		if versions[i].VersionString == versionStr {
			return &versions[i]
		}
	}
	return nil
}
