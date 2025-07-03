// Package sqlite 提供 SQLite 数据源适配器的备份功能实现
// 文件位置: internal/adapter/datasource/sqlite/manager_backup.go
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// handleTriggerBackup 处理触发备份请求，将指定业务组的所有数据库文件打包为 ZIP
func (m *Manager) handleTriggerBackup(ctx context.Context, req *v1.RequestEnvelope) (proto.Message, error) {
	// 解包请求参数
	var backupReq v1.TriggerBackupRequest
	if err := req.Payload.UnmarshalTo(&backupReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 TriggerBackupRequest 失败: %v", err)
	}
	// 校验备份目标目录
	if backupReq.BackupDestinationDir == "" {
		return nil, status.Error(codes.InvalidArgument, "备份目标目录不能为空")
	}
	// 创建备份目录
	if err := os.MkdirAll(backupReq.BackupDestinationDir, 0755); err != nil {
		return nil, status.Errorf(codes.Internal, "创建备份目录失败: %v", err)
	}
	// 生成备份文件路径
	backupFileName := fmt.Sprintf("%s-backup-%s.zip", req.BizName, time.Now().Format("20060102150405"))
	finalBackupPath := filepath.Join(backupReq.BackupDestinationDir, backupFileName)
	// 获取业务组的数据库路径列表
	m.mu.RLock()
	dbInstances, bizExists := m.group[req.BizName]
	m.mu.RUnlock()
	if !bizExists || len(dbInstances) == 0 {
		return nil, status.Errorf(codes.NotFound, "未找到业务 '%s' 的数据库实例进行备份", req.BizName)
	}
	var dbPaths []string
	for _, instance := range dbInstances {
		dbPaths = append(dbPaths, instance.path)
	}
	// 创建 ZIP 文件并写入数据库文件
	zipFile, err := os.Create(finalBackupPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "创建备份文件失败: %v", err)
	}
	defer zipFile.Close()
	zipWriter := zip.NewWriter(zipFile)
	for _, path := range dbPaths {
		if err := addFileToZip(zipWriter, path); err != nil {
			zipWriter.Close()
			return nil, status.Errorf(codes.Internal, "添加文件 '%s' 到备份失败: %v", path, err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		return nil, status.Errorf(codes.Internal, "关闭 ZIP 写入器失败: %v", err)
	}
	// 获取备份文件信息
	info, err := zipFile.Stat()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "获取备份文件信息失败: %v", err)
	}
	// 返回备份结果
	return &v1.TriggerBackupResult{
		FinalBackupPath: finalBackupPath,
		FileSize:        info.Size(),
		DbFilesCount:    int32(len(dbPaths)),
	}, nil
}

// addFileToZip 将指定文件写入 ZIP 存档
func addFileToZip(zipWriter *zip.Writer, filePath string) error {
	fileToZip, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer fileToZip.Close()
	info, err := fileToZip.Stat()
	if err != nil {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.Base(filePath)
	header.Method = zip.Deflate
	writer, err := zipWriter.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, fileToZip)
	return err
}
