// Package sqlite 提供 SQLite 数据源适配器的备份功能测试
// 文件位置: internal/adapter/datasource/sqlite/manager_backup_test.go
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// TestHandleTriggerBackup 测试 SQLite Manager 的备份逻辑
func TestHandleTriggerBackup(t *testing.T) {
	const bizName = "backup_test_biz"

	// 场景 1：正常备份两个数据库文件
	t.Run("成功备份两个数据库文件", func(t *testing.T) {
		// 创建两个测试数据库
		libs := []setupInfo{
			{LibName: "logs_2025_06_22", Schema: `CREATE TABLE log(msg TEXT);`},
			{LibName: "logs_2025_06_23", Schema: `CREATE TABLE log(msg TEXT);`},
		}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()

		// 创建临时备份目录
		backupDestDir, err := os.MkdirTemp("", "backup_dest_")
		require.NoError(t, err)
		defer os.RemoveAll(backupDestDir)

		// 构造请求信封
		reqPayload := &v1.TriggerBackupRequest{BackupDestinationDir: backupDestDir}
		packedPayload, err := anypb.New(reqPayload)
		require.NoError(t, err)
		reqEnvelope := &v1.RequestEnvelope{BizName: bizName, Payload: packedPayload}

		// 执行备份
		resPayload, err := manager.handleTriggerBackup(context.Background(), reqEnvelope)

		// 验证返回结果
		require.NoError(t, err)
		result, ok := resPayload.(*v1.TriggerBackupResult)
		require.True(t, ok)
		assert.Equal(t, int32(2), result.DbFilesCount)
		assert.Contains(t, result.FinalBackupPath, bizName)
		assert.True(t, result.FileSize > 0)
		assert.FileExists(t, result.FinalBackupPath)

		// 检查 ZIP 内容
		zipReader, err := zip.OpenReader(result.FinalBackupPath)
		require.NoError(t, err)
		defer zipReader.Close()
		assert.Len(t, zipReader.File, 2)

		foundFiles := make(map[string]bool)
		for _, f := range zipReader.File {
			foundFiles[f.Name] = true
		}
		assert.Contains(t, foundFiles, "logs_2025_06_22.db")
		assert.Contains(t, foundFiles, "logs_2025_06_23.db")
	})

	// 场景 2：各种错误情况的处理
	t.Run("错误场景处理", func(t *testing.T) {
		libs := []setupInfo{{LibName: "lib1", Schema: `CREATE TABLE t(id INT);`}}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()

		backupDestDir, err := os.MkdirTemp("", "backup_dest_err_")
		require.NoError(t, err)
		defer os.RemoveAll(backupDestDir)

		// 占位文件用于制造路径冲突
		placeholderFile, err := os.Create(filepath.Join(backupDestDir, "file.txt"))
		require.NoError(t, err)
		placeholderFile.Close()

		testCases := []struct {
			name           string
			bizName        string
			destDir        string
			expectedCode   codes.Code
			expectedErrMsg string
		}{
			{
				name:           "请求不存在的业务",
				bizName:        "non_existent_biz",
				destDir:        backupDestDir,
				expectedCode:   codes.NotFound,
				expectedErrMsg: "未找到业务 'non_existent_biz' 的任何数据库实例进行备份",
			},
			{
				name:           "目标目录为空",
				bizName:        bizName,
				destDir:        "",
				expectedCode:   codes.InvalidArgument,
				expectedErrMsg: "备份目标目录 'backup_destination_dir' 不能为空",
			},
			{
				name:           "无法创建目标目录",
				bizName:        bizName,
				destDir:        filepath.Join(backupDestDir, "file.txt/cannot_create"),
				expectedCode:   codes.Internal,
				expectedErrMsg: "创建备份目录失败",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				reqPayload := &v1.TriggerBackupRequest{BackupDestinationDir: tc.destDir}
				packedPayload, _ := anypb.New(reqPayload)
				reqEnvelope := &v1.RequestEnvelope{BizName: tc.bizName, Payload: packedPayload}

				_, err := manager.handleTriggerBackup(context.Background(), reqEnvelope)

				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tc.expectedCode, st.Code())
				assert.Contains(t, st.Message(), tc.expectedErrMsg)
			})
		}
	})
}
