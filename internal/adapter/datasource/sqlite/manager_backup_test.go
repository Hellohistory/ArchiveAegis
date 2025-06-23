// file: internal/adapter/datasource/sqlite/manager_backup_test.go
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

func TestHandleTriggerBackup(t *testing.T) {
	const bizName = "backup_test_biz"

	// --- 场景 1: 成功备份 ---
	t.Run("成功备份两个数据库文件", func(t *testing.T) {
		// Arrange (准备)
		// 1. 创建测试数据库环境
		libs := []setupInfo{
			{LibName: "logs_2025_06_22", Schema: `CREATE TABLE log(msg TEXT);`},
			{LibName: "logs_2025_06_23", Schema: `CREATE TABLE log(msg TEXT);`},
		}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()

		// 2. 创建一个临时的备份目标目录
		backupDestDir, err := os.MkdirTemp("", "backup_dest_")
		require.NoError(t, err, "创建备份目标目录不应失败")
		defer os.RemoveAll(backupDestDir)

		// 3. 构建请求
		reqPayload := &v1.TriggerBackupRequest{BackupDestinationDir: backupDestDir}
		packedPayload, err := anypb.New(reqPayload)
		require.NoError(t, err)
		reqEnvelope := &v1.RequestEnvelope{BizName: bizName, Payload: packedPayload}

		// Act (执行)
		resPayload, err := manager.handleTriggerBackup(context.Background(), reqEnvelope)

		// Assert (断言)
		// 1. 检查函数调用是否成功
		require.NoError(t, err, "handleTriggerBackup 不应返回错误")
		result, ok := resPayload.(*v1.TriggerBackupResult)
		require.True(t, ok, "响应载荷应为 *TriggerBackupResult 类型")

		// 2. 检查返回的元数据是否正确
		assert.Equal(t, int32(2), result.DbFilesCount, "备份文件计数应为 2")
		assert.Contains(t, result.FinalBackupPath, bizName, "备份文件名应包含业务名")
		assert.True(t, result.FileSize > 0, "备份文件大小应大于 0")

		// 3. [核心断言] 检查物理文件和其内容
		assert.FileExists(t, result.FinalBackupPath, "备份 zip 文件应在磁盘上存在")

		// 3a. 打开并读取 zip 文件进行内容验证
		zipReader, err := zip.OpenReader(result.FinalBackupPath)
		require.NoError(t, err, "打开创建的 zip 文件不应失败")
		defer zipReader.Close()

		// 3b. 验证 zip 文件中的文件数量
		assert.Len(t, zipReader.File, 2, "zip 压缩包中应包含 2 个文件")

		// 3c. 验证 zip 文件中的文件名是否正确
		foundFiles := make(map[string]bool)
		for _, f := range zipReader.File {
			foundFiles[f.Name] = true
		}
		assert.Contains(t, foundFiles, "logs_2025_06_22.db", "zip 中应包含第一个数据库文件")
		assert.Contains(t, foundFiles, "logs_2025_06_23.db", "zip 中应包含第二个数据库文件")
	})

	// --- 场景 2: 各种错误情况 ---
	t.Run("错误场景处理", func(t *testing.T) {
		// 为这个测试套件创建一个通用的环境
		libs := []setupInfo{{LibName: "lib1", Schema: `CREATE TABLE t(id INT);`}}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()

		backupDestDir, err := os.MkdirTemp("", "backup_dest_err_")
		require.NoError(t, err)
		defer os.RemoveAll(backupDestDir)

		testCases := []struct {
			name           string
			bizName        string
			destDir        string
			expectedCode   codes.Code
			expectedErrMsg string
		}{
			{
				name:           "请求一个不存在的业务",
				bizName:        "non_existent_biz",
				destDir:        backupDestDir,
				expectedCode:   codes.NotFound,
				expectedErrMsg: "未找到业务 'non_existent_biz' 的任何数据库实例进行备份",
			},
			{
				name:           "请求的备份目录为空字符串",
				bizName:        bizName,
				destDir:        "",
				expectedCode:   codes.InvalidArgument,
				expectedErrMsg: "备份目标目录 'backup_destination_dir' 不能为空",
			},
			{
				name:           "目标目录无法创建 (例如权限问题)",
				bizName:        bizName,
				destDir:        filepath.Join(backupDestDir, "file.txt/cannot_create"), // 尝试在文件下创建目录，会导致失败
				expectedCode:   codes.Internal,
				expectedErrMsg: "创建备份目录失败", // 错误信息包含这个前缀即可
			},
		}

		// 为了测试“目标目录无法创建”，我们先创建一个文件占位
		placeholderFile, err := os.Create(filepath.Join(backupDestDir, "file.txt"))
		require.NoError(t, err)
		placeholderFile.Close()

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				reqPayload := &v1.TriggerBackupRequest{BackupDestinationDir: tc.destDir}
				packedPayload, _ := anypb.New(reqPayload)
				reqEnvelope := &v1.RequestEnvelope{BizName: tc.bizName, Payload: packedPayload}

				_, err := manager.handleTriggerBackup(context.Background(), reqEnvelope)

				// 断言错误
				require.Error(t, err, "期望得到一个错误")
				st, ok := status.FromError(err)
				require.True(t, ok, "错误应该是 gRPC status 类型")
				assert.Equal(t, tc.expectedCode, st.Code(), "错误码不匹配")
				assert.Contains(t, st.Message(), tc.expectedErrMsg, "错误信息不匹配")
			})
		}
	})
}
