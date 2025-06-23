// Package sqlite file: internal/adapter/datasource/sqlite/manager_backup.go
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

func (m *Manager) handleTriggerBackup(ctx context.Context, req *v1.RequestEnvelope) (proto.Message, error) {
	var backupReq v1.TriggerBackupRequest
	if err := req.Payload.UnmarshalTo(&backupReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 TriggerBackupRequest 失败: %v", err)
	}

	if backupReq.BackupDestinationDir == "" {
		return nil, status.Error(codes.InvalidArgument, "备份目标目录 'backup_destination_dir' 不能为空")
	}

	if err := os.MkdirAll(backupReq.BackupDestinationDir, 0755); err != nil {
		return nil, status.Errorf(codes.Internal, "创建备份目录失败: %v", err)
	}

	backupFileName := fmt.Sprintf("%s-backup-%s.zip", req.BizName, time.Now().Format("20060102150405"))
	finalBackupPath := filepath.Join(backupReq.BackupDestinationDir, backupFileName)

	m.mu.RLock()
	dbInstances, bizExists := m.group[req.BizName]
	if !bizExists || len(dbInstances) == 0 {
		m.mu.RUnlock()
		return nil, status.Errorf(codes.NotFound, "未找到业务 '%s' 的任何数据库实例进行备份", req.BizName)
	}

	var dbPaths []string
	for _, instance := range dbInstances {
		dbPaths = append(dbPaths, instance.path)
	}
	m.mu.RUnlock() // 尽快释放锁

	zipFile, err := os.Create(finalBackupPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "创建备份 zip 文件失败: %v", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	for _, path := range dbPaths {
		if err := addFileToZip(zipWriter, path); err != nil {
			return nil, status.Errorf(codes.Internal, "添加文件 '%s' 到备份中失败: %v", path, err)
		}
	}

	info, err := zipFile.Stat()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "获取备份文件信息失败: %v", err)
	}

	return &v1.TriggerBackupResult{
		FinalBackupPath: finalBackupPath,
		FileSize:        info.Size(),
		DbFilesCount:    int32(len(dbPaths)),
	}, nil
}

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
