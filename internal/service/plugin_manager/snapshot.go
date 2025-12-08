// file: internal/service/plugin_manager/snapshot.go

// Package plugin_manager 提供插件管理器的实现，包括插件目录、安装和生命周期管理功能
package plugin_manager

import (
	"context"
	"fmt"
	"time"

	"ArchiveAegis/internal/core/domain"
)

// Snapshot 描述插件管理相关的持久状态，便于 Raft 快照序列化。
type Snapshot struct {
	InstalledPlugins []InstalledPluginRecord `json:"installed_plugins"`
	PluginInstances  []domain.PluginInstance `json:"plugin_instances"`
}

// InstalledPluginRecord 记录已安装插件的版本与位置。
type InstalledPluginRecord struct {
	PluginID    string    `json:"plugin_id"`
	Version     string    `json:"version"`
	InstallPath string    `json:"install_path"`
	InstalledAt time.Time `json:"installed_at"`
}

// SnapshotState 导出插件安装与实例配置的快照。
func (pm *PluginManager) SnapshotState(ctx context.Context) (*Snapshot, error) {
	installed, err := pm.loadInstalledPlugins(ctx)
	if err != nil {
		return nil, err
	}
	instances, err := pm.loadPluginInstances(ctx)
	if err != nil {
		return nil, err
	}
	return &Snapshot{InstalledPlugins: installed, PluginInstances: instances}, nil
}

// RestoreState 用快照内容覆盖当前的插件相关存储。
func (pm *PluginManager) RestoreState(ctx context.Context, snapshot *Snapshot) error {
	tx, err := pm.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启插件快照事务失败: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err = tx.ExecContext(ctx, "DELETE FROM plugin_instances"); err != nil {
		return fmt.Errorf("清空插件实例表失败: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM installed_plugins"); err != nil {
		return fmt.Errorf("清空已安装插件表失败: %w", err)
	}

	const insertInstalled = `INSERT INTO installed_plugins (plugin_id, version, install_path, installed_at) VALUES (?, ?, ?, ?)`
	for _, record := range snapshot.InstalledPlugins {
		if _, err = tx.ExecContext(ctx, insertInstalled, record.PluginID, record.Version, record.InstallPath, record.InstalledAt); err != nil {
			return fmt.Errorf("恢复已安装插件 '%s@%s' 失败: %w", record.PluginID, record.Version, err)
		}
	}

	const insertInstance = `INSERT INTO plugin_instances (instance_id, display_name, plugin_id, version, biz_name, port, status, enabled, created_at, last_started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, inst := range snapshot.PluginInstances {
		if _, err = tx.ExecContext(ctx, insertInstance, inst.InstanceID, inst.DisplayName, inst.PluginID, inst.Version, inst.BizName, inst.Port, inst.Status, inst.Enabled, inst.CreatedAt, inst.LastStartedAt); err != nil {
			return fmt.Errorf("恢复插件实例 '%s' 失败: %w", inst.InstanceID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交插件快照事务失败: %w", err)
	}
	return nil
}

func (pm *PluginManager) loadInstalledPlugins(ctx context.Context) ([]InstalledPluginRecord, error) {
	const query = `SELECT plugin_id, version, install_path, installed_at FROM installed_plugins`
	rows, err := pm.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("查询已安装插件失败: %w", err)
	}
	defer rows.Close()

	var records []InstalledPluginRecord
	for rows.Next() {
		var record InstalledPluginRecord
		if err := rows.Scan(&record.PluginID, &record.Version, &record.InstallPath, &record.InstalledAt); err != nil {
			return nil, fmt.Errorf("扫描已安装插件行失败: %w", err)
		}
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (pm *PluginManager) loadPluginInstances(ctx context.Context) ([]domain.PluginInstance, error) {
	const query = `SELECT instance_id, display_name, plugin_id, version, biz_name, port, status, enabled, created_at, last_started_at FROM plugin_instances`
	rows, err := pm.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("查询插件实例失败: %w", err)
	}
	defer rows.Close()

	var instances []domain.PluginInstance
	for rows.Next() {
		var inst domain.PluginInstance
		if err := rows.Scan(&inst.InstanceID, &inst.DisplayName, &inst.PluginID, &inst.Version, &inst.BizName, &inst.Port, &inst.Status, &inst.Enabled, &inst.CreatedAt, &inst.LastStartedAt); err != nil {
			return nil, fmt.Errorf("扫描插件实例行失败: %w", err)
		}
		instances = append(instances, inst)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return instances, nil
}
