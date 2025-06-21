// file: internal/adapter/datasource/sqlite/main_test.go
package sqlite

import (
	"ArchiveAegis/internal/core/domain"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// ============================================================================
//  共享测试辅助工具 (Shared Test Helpers & Mocks)
// ============================================================================

// mockAdminConfigService 是 port.QueryAdminConfigService 接口的一个测试替身
// 这个定义将在这个包的所有测试文件中共享。
type mockAdminConfigService struct {
	GetBizQueryConfigFunc           func(ctx context.Context, bizName string) (*domain.BizQueryConfig, error)
	UpdateBizOverallSettingsFunc    func(ctx context.Context, bizName string, settings domain.BizOverallSettings) error
	UpdateBizSearchableTablesFunc   func(ctx context.Context, bizName string, tableNames []string) error
	UpdateTableFieldSettingsFunc    func(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) error
	UpdateTableWritePermissionsFunc func(ctx context.Context, bizName, tableName string, perms domain.TableConfig) error
	GetAllConfiguredBizNamesFunc    func(ctx context.Context) ([]string, error)
	GetDefaultViewConfigFunc        func(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error)
	GetAllViewConfigsForBizFunc     func(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error)
	UpdateAllViewsForBizFunc        func(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) error
	GetIPLimitSettingsFunc          func(ctx context.Context) (*domain.IPLimitSetting, error)
	UpdateIPLimitSettingsFunc       func(ctx context.Context, settings domain.IPLimitSetting) error
	GetUserLimitSettingsFunc        func(ctx context.Context, userID int64) (*domain.UserLimitSetting, error)
	UpdateUserLimitSettingsFunc     func(ctx context.Context, userID int64, settings domain.UserLimitSetting) error
	GetBizRateLimitSettingsFunc     func(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error)
	UpdateBizRateLimitSettingsFunc  func(ctx context.Context, bizName string, settings domain.BizRateLimitSetting) error
	InvalidateCacheForBizFunc       func(bizName string)
	InvalidateAllCachesFunc         func()
}

func (m *mockAdminConfigService) GetBizQueryConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	if m.GetBizQueryConfigFunc != nil {
		return m.GetBizQueryConfigFunc(ctx, bizName)
	}
	return nil, nil // 默认返回 nil
}
func (m *mockAdminConfigService) UpdateBizOverallSettings(ctx context.Context, bizName string, settings domain.BizOverallSettings) error {
	return nil
}
func (m *mockAdminConfigService) UpdateBizSearchableTables(ctx context.Context, bizName string, tableNames []string) error {
	return nil
}
func (m *mockAdminConfigService) UpdateTableFieldSettings(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) error {
	return nil
}
func (m *mockAdminConfigService) UpdateTableWritePermissions(ctx context.Context, bizName, tableName string, perms domain.TableConfig) error {
	return nil
}
func (m *mockAdminConfigService) GetAllConfiguredBizNames(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockAdminConfigService) GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) error {
	return nil
}
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
func (m *mockAdminConfigService) InvalidateCacheForBiz(bizName string) {}
func (m *mockAdminConfigService) InvalidateAllCaches()                 {}

// createTestDB 创建一个带有指定 schema 的临时数据库文件。
// 这个定义将在这个包的所有测试文件中共享。
func createTestDB(t *testing.T, dir, filename string, createStmts ...string) *sql.DB {
	t.Helper()
	path := filepath.Join(dir, filename)

	// ====================================================================
	// 核心修正点：使用与生产代码更接近的 DSN，特别是启用 WAL 模式来避免锁定问题
	// ====================================================================
	dsn := "file:" + path + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)

	for _, stmt := range createStmts {
		_, err = db.Exec(stmt)
		require.NoError(t, err, "Failed to execute statement: %s", stmt)
	}

	// t.Cleanup 会在每个测试（或子测试）结束时自动执行清理代码
	t.Cleanup(func() {
		db.Close()
	})

	return db
}
