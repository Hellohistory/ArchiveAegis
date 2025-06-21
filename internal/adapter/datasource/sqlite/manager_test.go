// file: internal/adapter/datasource/sqlite/manager_test.go
package sqlite

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// 注意：mockAdminConfigService 和 createTestDB 的定义已经被移到 main_test.go 中，此处不再需要。

func TestManager_Query_TotalCount(t *testing.T) {
	ctx := context.Background()
	// 使用共享的 mock
	mockCfgSvc := &mockAdminConfigService{
		GetBizQueryConfigFunc: func(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
			return &domain.BizQueryConfig{
				BizName:              bizName,
				IsPubliclySearchable: true,
				DefaultQueryTable:    "test_data",
				Tables: map[string]*domain.TableConfig{
					"test_data": {
						TableName:    "test_data",
						IsSearchable: true,
						Fields: map[string]domain.FieldSetting{
							"id":   {FieldName: "id", IsReturnable: true, IsSearchable: true},
							"data": {FieldName: "data", IsReturnable: true, IsSearchable: true},
						},
					},
				},
			}, nil
		},
	}

	instanceDir := t.TempDir()
	bizDir := filepath.Join(instanceDir, "test_biz")
	require.NoError(t, os.Mkdir(bizDir, 0755))

	// 使用新的、共享的 createTestDB 辅助函数
	db1 := createTestDB(t, bizDir, "db1.db", `CREATE TABLE test_data (id INTEGER PRIMARY KEY, data TEXT);`)
	for i := 0; i < 3; i++ {
		_, err := db1.Exec(`INSERT INTO test_data (data) VALUES (?)`, fmt.Sprintf("row-%d", i))
		require.NoError(t, err)
	}

	db2 := createTestDB(t, bizDir, "db2.db", `CREATE TABLE test_data (id INTEGER PRIMARY KEY, data TEXT);`)
	for i := 0; i < 5; i++ {
		_, err := db2.Exec(`INSERT INTO test_data (data) VALUES (?)`, fmt.Sprintf("row-%d", i))
		require.NoError(t, err)
	}

	manager := NewManager(mockCfgSvc)
	defer manager.Close()

	require.NoError(t, manager.InitForBiz(ctx, instanceDir, "test_biz"))

	queryReq := port.QueryRequest{
		BizName:   "test_biz",
		TableName: "test_data",
		Page:      1,
		Size:      10,
	}
	result, err := manager.Query(ctx, queryReq)

	require.NoError(t, err)
	require.NotNil(t, result)

	expectedTotal := int64(8)
	assert.Equal(t, expectedTotal, result.Total, "Total count mismatch")
	assert.Len(t, result.Data, int(expectedTotal), "Returned data length mismatch")
}

func TestManager_Mutate_FailFast(t *testing.T) {
	ctx := context.Background()

	mockCfgSvc := &mockAdminConfigService{
		GetBizQueryConfigFunc: func(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
			return &domain.BizQueryConfig{
				BizName:              bizName,
				IsPubliclySearchable: true,
				Tables: map[string]*domain.TableConfig{
					"test_data": {
						TableName:    "test_data",
						AllowCreate:  true, // 允许创建
						IsSearchable: true,
						Fields:       map[string]domain.FieldSetting{"id": {IsReturnable: true}, "data": {IsReturnable: true}},
					},
				},
			}, nil
		},
	}

	instanceDir := t.TempDir()
	bizDir := filepath.Join(instanceDir, "fail_fast_biz")
	require.NoError(t, os.Mkdir(bizDir, 0755))

	// DB1: 普通表
	createTestDB(t, bizDir, "db1.db", `CREATE TABLE test_data (id INTEGER PRIMARY KEY, data TEXT);`)

	// DB2: 有唯一约束的表，这将导致第二次插入失败
	dbWithConstraint := createTestDB(t, bizDir, "db2.db", `CREATE TABLE test_data (id INTEGER PRIMARY KEY, data TEXT UNIQUE);`)
	_, err := dbWithConstraint.Exec(`INSERT INTO test_data (data) VALUES (?)`, "unique_value")
	require.NoError(t, err)

	manager := NewManager(mockCfgSvc)
	defer manager.Close()
	require.NoError(t, manager.InitForBiz(ctx, instanceDir, "fail_fast_biz"))

	// 这个操作在 db1 会成功，但在 db2 会因为违反唯一约束而失败
	mutateReq := port.MutateRequest{
		BizName: "fail_fast_biz",
		CreateOp: &port.CreateOperation{
			TableName: "test_data",
			Data: map[string]interface{}{
				"data": "unique_value", // 这个值在db2中已经存在
			},
		},
	}
	_, err = manager.Mutate(ctx, mutateReq)

	require.Error(t, err, "manager.Mutate was expected to fail, but it succeeded")

	expectedErrorSubstring := "操作在库 'db2' 上失败并已中止"
	assert.True(t, strings.Contains(err.Error(), expectedErrorSubstring),
		fmt.Sprintf("Error message mismatch:\nGot:  %v\nWant to contain: %s", err, expectedErrorSubstring))
}
