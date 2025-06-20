// file: internal/adapter/datasource/sqlite/helpers_test.go
package sqlite

import (
	"ArchiveAegis/internal/core/port"
	"reflect"
	"testing"
)

func TestBuildCountSQL(t *testing.T) {
	// 定义一系列测试用例
	testCases := []struct {
		name         string
		tableName    string
		queryParams  []port.QueryParam
		expectedSQL  string
		expectedArgs []any
		expectError  bool
	}{
		{
			name:         "Simple count without filters",
			tableName:    "users",
			queryParams:  []port.QueryParam{},
			expectedSQL:  `SELECT COUNT(*) FROM "users"`,
			expectedArgs: []any{},
			expectError:  false,
		},
		{
			name:      "Count with one filter",
			tableName: "logs",
			queryParams: []port.QueryParam{
				{Field: "level", Value: "error"},
			},
			expectedSQL:  `SELECT COUNT(*) FROM "logs" WHERE "level" = ?`,
			expectedArgs: []any{"error"},
			expectError:  false,
		},
		{
			name:      "Count with fuzzy filter",
			tableName: "products",
			queryParams: []port.QueryParam{
				{Field: "name", Value: "widget", Fuzzy: true},
			},
			expectedSQL:  `SELECT COUNT(*) FROM "products" WHERE "name" LIKE ?`,
			expectedArgs: []any{"%widget%"},
			expectError:  false,
		},
		{
			name:      "Count with multiple filters and logic",
			tableName: "events",
			queryParams: []port.QueryParam{
				{Field: "type", Value: "login", Logic: "AND"},
				{Field: "user_id", Value: "123"},
			},
			expectedSQL:  `SELECT COUNT(*) FROM "events" WHERE "type" = ? AND "user_id" = ?`,
			expectedArgs: []any{"login", "123"},
			expectError:  false,
		},
		{
			name:        "Empty table name should error",
			tableName:   "",
			queryParams: []port.QueryParam{},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// t.Parallel() // 如果测试之间独立，可以并行运行

			sql, args, err := buildCountSQL(tc.tableName, tc.queryParams)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error, but got nil")
				}
				return // 测试结束
			}

			if err != nil {
				t.Fatalf("Did not expect an error, but got: %v", err)
			}

			if sql != tc.expectedSQL {
				t.Errorf("SQL mismatch:\nGot:  %s\nWant: %s", sql, tc.expectedSQL)
			}

			if !reflect.DeepEqual(args, tc.expectedArgs) {
				t.Errorf("Args mismatch:\nGot:  %v\nWant: %v", args, tc.expectedArgs)
			}
		})
	}
}
