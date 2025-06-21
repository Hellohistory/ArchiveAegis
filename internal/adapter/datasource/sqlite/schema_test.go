// file: internal/adapter/datasource/sqlite/schema_test.go
package sqlite

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// 注意: mockAdminConfigService 和 createTestDB 的定义已经被移到 main_test.go 中

func TestGetSchema(t *testing.T) {
	mockCfgSvc := &mockAdminConfigService{}
	manager := NewManager(mockCfgSvc)

	bizConfig := &domain.BizQueryConfig{
		BizName: "sales",
		Tables: map[string]*domain.TableConfig{
			"orders": {
				TableName: "orders",
				Fields: map[string]domain.FieldSetting{
					"id":   {FieldName: "id", DataType: "INTEGER", IsReturnable: true, IsSearchable: true},
					"item": {FieldName: "item", DataType: "TEXT", IsReturnable: true, IsSearchable: false},
				},
			},
		},
	}

	t.Run("happy path", func(t *testing.T) {
		mockCfgSvc.GetBizQueryConfigFunc = func(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
			return bizConfig, nil
		}
		req := port.SchemaRequest{BizName: "sales"}
		result, err := manager.GetSchema(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Len(t, result.Tables, 1)
		assert.Len(t, result.Tables["orders"], 2)
		assert.Equal(t, "id", result.Tables["orders"][0].Name)
		assert.Equal(t, "item", result.Tables["orders"][1].Name)
	})

	t.Run("biz not found", func(t *testing.T) {
		mockCfgSvc.GetBizQueryConfigFunc = func(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
			return nil, nil // 模拟业务不存在
		}
		req := port.SchemaRequest{BizName: "nonexistent"}
		_, err := manager.GetSchema(context.Background(), req)
		assert.ErrorIs(t, err, port.ErrBizNotFound)
	})

	t.Run("config service error", func(t *testing.T) {
		mockCfgSvc.GetBizQueryConfigFunc = func(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
			return nil, errors.New("database connection failed")
		}
		req := port.SchemaRequest{BizName: "sales"}
		_, err := manager.GetSchema(context.Background(), req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database connection failed")
	})

	t.Run("table not found in biz", func(t *testing.T) {
		mockCfgSvc.GetBizQueryConfigFunc = func(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
			return bizConfig, nil
		}
		req := port.SchemaRequest{BizName: "sales", TableName: "nonexistent_table"}
		_, err := manager.GetSchema(context.Background(), req)
		assert.ErrorIs(t, err, port.ErrTableNotFoundInBiz)
	})
}

func TestReadWriteSchemaCache(t *testing.T) {
	tempDir := t.TempDir()
	bizDir := filepath.Join(tempDir, "test_biz")
	require.NoError(t, os.Mkdir(bizDir, 0755))

	expectedLibs := map[string]map[string][]string{
		"db1": {"table1": {"colA", "colB"}},
	}
	expectedTables := map[string][]string{
		"table1": {"colA", "colB"},
	}

	t.Run("write and read success", func(t *testing.T) {
		err := writeSchemaCache(bizDir, expectedLibs, expectedTables)
		require.NoError(t, err)

		cacheFile := filepath.Join(bizDir, schemaCacheFilename)
		data, err := os.ReadFile(cacheFile)
		require.NoError(t, err)

		var readData schemaFile
		err = json.Unmarshal(data, &readData)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), readData.UpdatedAt, 5*time.Second)
		assert.Equal(t, expectedTables, readData.Tables)
		assert.Equal(t, expectedLibs, readData.Libs)

		tables, libs, err := readSchemaCache(bizDir)
		require.NoError(t, err)
		assert.Equal(t, expectedTables, tables)
		assert.Equal(t, expectedLibs, libs)
	})

	t.Run("read non-existent file", func(t *testing.T) {
		nonExistentDir := filepath.Join(tempDir, "not_real")
		_, _, err := readSchemaCache(nonExistentDir)
		assert.Error(t, err)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("read malformed json", func(t *testing.T) {
		malformedFile := filepath.Join(bizDir, schemaCacheFilename)
		err := os.WriteFile(malformedFile, []byte("{not json"), 0644)
		require.NoError(t, err)

		_, _, err = readSchemaCache(bizDir)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid character")
	})
}

func TestLoadDBPhysicalSchema(t *testing.T) {
	db := createTestDB(t, t.TempDir(), "test.db",
		`CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT);`,
		`CREATE TABLE users (id INTEGER, email TEXT);`,
		`CREATE TABLE _archiveaegis_internal_meta (key TEXT, value TEXT);`,
	)

	info, err := loadDBPhysicalSchema(context.Background(), db)
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, "products", info.detectedDefaultTable)
	expectedTables := map[string][]string{
		"products": {"id", "name"},
		"users":    {"email", "id"},
	}
	assert.Equal(t, expectedTables, info.allTablesAndColumns)
}

func TestComputeSchemaUnionForBiz(t *testing.T) {
	manager := &Manager{
		dbSchemaCache: make(map[*sql.DB]*dbPhysicalSchemaInfo),
	}
	db1 := &sql.DB{}
	db2 := &sql.DB{}

	manager.dbSchemaCache[db1] = &dbPhysicalSchemaInfo{
		allTablesAndColumns: map[string][]string{
			"users":  {"id", "name"},
			"events": {"id", "timestamp"},
		},
	}
	manager.dbSchemaCache[db2] = &dbPhysicalSchemaInfo{
		allTablesAndColumns: map[string][]string{
			"users":  {"address", "id"},
			"orders": {"order_id", "user_id"},
		},
	}

	libsMap := map[string]*sql.DB{
		"lib1": db1,
		"lib2": db2,
	}

	union, perLib := manager.computeSchemaUnionForBiz("test_biz", libsMap)

	expectedUnion := map[string][]string{
		"users":  {"address", "id", "name"},
		"events": {"id", "timestamp"},
		"orders": {"order_id", "user_id"},
	}
	assert.Equal(t, expectedUnion, union)

	expectedPerLib := map[string]map[string][]string{
		"lib1": {
			"users":  {"id", "name"},
			"events": {"id", "timestamp"},
		},
		"lib2": {
			"users":  {"address", "id"},
			"orders": {"order_id", "user_id"},
		},
	}
	assert.Equal(t, expectedPerLib, perLib)
}
