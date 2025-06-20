// file: internal/adapter/datasource/sqlite/manager_test.go
package sqlite

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"context"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockAdminConfigService 是一个测试替身，它实现了 port.QueryAdminConfigService 接口
type mockAdminConfigService struct {
	// 定义函数字段，以便在测试中动态替换其行为
	GetBizQueryConfigFunc           func(ctx context.Context, bizName string) (*domain.BizQueryConfig, error)
	UpdateBizOverallSettingsFunc    func(ctx context.Context, bizName string, settings domain.BizOverallSettings) error
	UpdateBizSearchableTablesFunc   func(ctx context.Context, bizName string, tableNames []string) error
	UpdateTableFieldSettingsFunc    func(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) error
	UpdateTableWritePermissionsFunc func(ctx context.Context, bizName, tableName string, perms domain.TableConfig) error
}

// GetBizQueryConfig 调用对应的函数字段，如果未设置，则返回一个默认的只读配置
func (m *mockAdminConfigService) GetBizQueryConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	if m.GetBizQueryConfigFunc != nil {
		return m.GetBizQueryConfigFunc(ctx, bizName)
	}
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
}

// ✅ FIX: 添加所有在 port.QueryAdminConfigService 接口中新增的方法的桩实现

func (m *mockAdminConfigService) UpdateBizOverallSettings(ctx context.Context, bizName string, settings domain.BizOverallSettings) error {
	if m.UpdateBizOverallSettingsFunc != nil {
		return m.UpdateBizOverallSettingsFunc(ctx, bizName, settings)
	}
	return nil
}

func (m *mockAdminConfigService) UpdateBizSearchableTables(ctx context.Context, bizName string, tableNames []string) error {
	if m.UpdateBizSearchableTablesFunc != nil {
		return m.UpdateBizSearchableTablesFunc(ctx, bizName, tableNames)
	}
	return nil
}

func (m *mockAdminConfigService) UpdateTableFieldSettings(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) error {
	if m.UpdateTableFieldSettingsFunc != nil {
		return m.UpdateTableFieldSettingsFunc(ctx, bizName, tableName, fields)
	}
	return nil
}

func (m *mockAdminConfigService) UpdateTableWritePermissions(ctx context.Context, bizName, tableName string, perms domain.TableConfig) error {
	if m.UpdateTableWritePermissionsFunc != nil {
		return m.UpdateTableWritePermissionsFunc(ctx, bizName, tableName, perms)
	}
	return nil
}

// --- 其他已有的桩实现 ---

func (m *mockAdminConfigService) GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) error {
	return nil
}
func (m *mockAdminConfigService) GetAllConfiguredBizNames(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockAdminConfigService) InvalidateCacheForBiz(bizName string) {}
func (m *mockAdminConfigService) InvalidateAllCaches()                 {}
func (m *mockAdminConfigService) GetIPLimitSettings(ctx context.Context) (*domain.IPLimitSetting, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateIPLimitSettings(ctx context.Context, settings domain.IPLimitSetting) error {
	return nil
}
func (m *mockAdminConfigService) GetUserLimitSettings(ctx context.Context, userID int64) (*domain.UserLimitSetting, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateUserLimitSettings(ctx context.Context, userID int64, settings domain.UserLimitSetting) error {
	return nil
}
func (m *mockAdminConfigService) GetBizRateLimitSettings(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings domain.BizRateLimitSetting) error {
	return nil
}

// createTestDB 是一个辅助函数，用于创建一个临时的SQLite数据库并填充数据。
func createTestDB(t *testing.T, dir, filename string, numRows int) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_busy_timeout=5000", path))
	if err != nil {
		t.Fatalf("Failed to open test db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE test_data (id INTEGER PRIMARY KEY, data TEXT);`)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	for i := 0; i < numRows; i++ {
		_, err = db.Exec(`INSERT INTO test_data (data) VALUES (?)`, fmt.Sprintf("row-%d", i))
		if err != nil {
			t.Fatalf("Failed to insert data: %v", err)
		}
	}
	return path
}

func TestManager_Query_TotalCount(t *testing.T) {
	ctx := context.Background()
	mockCfgSvc := &mockAdminConfigService{}

	instanceDir := t.TempDir()
	bizDir := filepath.Join(instanceDir, "test_biz")
	if err := os.Mkdir(bizDir, 0755); err != nil {
		t.Fatalf("Failed to create biz dir: %v", err)
	}

	createTestDB(t, bizDir, "db1.db", 3)
	createTestDB(t, bizDir, "db2.db", 5)

	manager := NewManager(mockCfgSvc)
	defer manager.Close()

	if err := manager.InitForBiz(ctx, instanceDir, "test_biz"); err != nil {
		t.Fatalf("Manager InitForBiz failed: %v", err)
	}

	queryReq := port.QueryRequest{
		BizName:   "test_biz",
		TableName: "test_data",
		Page:      1,
		Size:      10,
	}
	result, err := manager.Query(ctx, queryReq)

	if err != nil {
		t.Fatalf("manager.Query returned an unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("manager.Query returned a nil result")
	}

	expectedTotal := int64(8)
	if result.Total != expectedTotal {
		t.Errorf("Total count mismatch: Got %d, Want %d", result.Total, expectedTotal)
	}
	if len(result.Data) != int(expectedTotal) {
		t.Errorf("Returned data length mismatch: Got %d, Want %d", len(result.Data), expectedTotal)
	}
}

// TestManager_Mutate_FailFast 测试写操作在某个库失败时，是否会快速失败并返回特定错误。
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
	if err := os.Mkdir(bizDir, 0755); err != nil {
		t.Fatalf("Failed to create biz dir: %v", err)
	}

	// DB1: 普通表
	createTestDB(t, bizDir, "db1.db", 0)

	// DB2: 有唯一约束的表，这将导致第二次插入失败
	db2Path := filepath.Join(bizDir, "db2.db")
	db2, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_busy_timeout=5000", db2Path))
	if err != nil {
		t.Fatalf("Failed to open test db2: %v", err)
	}
	_, err = db2.Exec(`CREATE TABLE test_data (id INTEGER PRIMARY KEY, data TEXT UNIQUE);`)
	if err != nil {
		t.Fatalf("Failed to create unique table in db2: %v", err)
	}
	_, err = db2.Exec(`INSERT INTO test_data (data) VALUES (?)`, "unique_value")
	if err != nil {
		t.Fatalf("Failed to insert initial data in db2: %v", err)
	}
	db2.Close()

	manager := NewManager(mockCfgSvc)
	defer manager.Close()
	if err := manager.InitForBiz(ctx, instanceDir, "fail_fast_biz"); err != nil {
		t.Fatalf("Manager InitForBiz failed: %v", err)
	}

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

	if err == nil {
		t.Fatal("manager.Mutate was expected to fail, but it succeeded")
	}

	expectedErrorSubstring := "操作在库 'db2' 上失败并已中止"
	if !strings.Contains(err.Error(), expectedErrorSubstring) {
		t.Errorf("Error message mismatch:\nGot:  %v\nWant to contain: %s", err, expectedErrorSubstring)
	}
}
