// file: internal/adapter/datasource/sqlite/helpers_test.go

package sqlite

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// -----------------------------------------------------------------------------
// buildQuerySQL / buildCountSQL
// -----------------------------------------------------------------------------

func TestBuildQuerySQL(t *testing.T) {
	filters := []queryParam{
		{Field: "name", Value: "John", Fuzzy: false},
	}
	sqlStr, args, err := buildQuerySQL("users", []string{"id", "name"}, filters, 2, 10)
	if err != nil {
		t.Fatalf("buildQuerySQL 返回错误: %v", err)
	}

	wantSQL := `SELECT "id", "name" FROM "users" WHERE "name" = ? LIMIT ? OFFSET ?`
	if sqlStr != wantSQL {
		t.Errorf("SQL 不匹配\n  got : %s\n  want: %s", sqlStr, wantSQL)
	}

	wantArgs := []any{"John", 10, 10} // page=2,size=10 → offset=(2-1)*10=10
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("参数不匹配\n  got : %#v\n  want: %#v", args, wantArgs)
	}
}

func TestBuildQuerySQL_Defaults(t *testing.T) {
	// page<1 与 size<1 应触发默认值 page=1,size=50
	sqlStr, args, err := buildQuerySQL("tbl", []string{"x"}, nil, 0, 0)
	if err != nil {
		t.Fatalf("buildQuerySQL 返回错误: %v", err)
	}
	want := []any{50, 0}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("默认 page/size 处理错误, got=%#v", args)
	}
	if !strings.HasSuffix(sqlStr, "LIMIT ? OFFSET ?") {
		t.Errorf("SQL 末尾应包含 LIMIT 与 OFFSET, got=%s", sqlStr)
	}
}

func TestBuildCountSQL(t *testing.T) {
	sqlStr, args, err := buildCountSQL("orders", []queryParam{
		{Field: "status", Value: "PAID"},
	})
	if err != nil {
		t.Fatalf("buildCountSQL 错误: %v", err)
	}
	wantSQL := `SELECT COUNT(*) FROM "orders" WHERE "status" = ?`
	if sqlStr != wantSQL {
		t.Errorf("SQL 不匹配: got=%s", sqlStr)
	}
	if len(args) != 1 || args[0] != "PAID" {
		t.Errorf("参数不匹配, got=%v", args)
	}
}

// -----------------------------------------------------------------------------
// buildInsertSQL / buildUpdateSQL / buildDeleteSQL
// -----------------------------------------------------------------------------

func TestBuildInsertSQL(t *testing.T) {
	sqlStr, args, err := buildInsertSQL("users", map[string]interface{}{"age": 30, "name": "John"})
	if err != nil {
		t.Fatalf("buildInsertSQL 错误: %v", err)
	}
	wantSQL := `INSERT INTO "users" ("age", "name") VALUES (?, ?)`
	if sqlStr != wantSQL {
		t.Errorf("SQL 不匹配: got=%s", sqlStr)
	}
	wantArgs := []interface{}{30, "John"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("参数不匹配: %#v", args)
	}
}

func TestBuildUpdateSQL(t *testing.T) {
	sqlStr, args, err := buildUpdateSQL("users",
		map[string]interface{}{"name": "Jane"},
		[]queryParam{{Field: "id", Value: "1"}},
	)
	if err != nil {
		t.Fatalf("buildUpdateSQL 错误: %v", err)
	}
	wantSQL := `UPDATE "users" SET "name" = ? WHERE "id" = ?`
	if sqlStr != wantSQL {
		t.Errorf("SQL 不匹配: got=%s", sqlStr)
	}
	wantArgs := []interface{}{"Jane", "1"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("参数不匹配: %#v", args)
	}
}

func TestBuildDeleteSQL(t *testing.T) {
	sqlStr, args, err := buildDeleteSQL("users", []queryParam{{Field: "id", Value: "1"}})
	if err != nil {
		t.Fatalf("buildDeleteSQL 错误: %v", err)
	}
	wantSQL := `DELETE FROM "users" WHERE "id" = ?`
	if sqlStr != wantSQL {
		t.Errorf("SQL 不匹配: got=%s", sqlStr)
	}
	if len(args) != 1 || args[0] != "1" {
		t.Errorf("参数不匹配: %v", args)
	}

	// 无过滤条件应报错
	if _, _, err = buildDeleteSQL("tbl", nil); err == nil {
		t.Error("空过滤条件未返回错误")
	}
}

// -----------------------------------------------------------------------------
// buildWhereClause
// -----------------------------------------------------------------------------

func TestBuildWhereClause_FuzzyAndLogic(t *testing.T) {
	clause, args, err := buildWhereClause([]queryParam{
		{Field: "name", Value: "ohn", Fuzzy: true, Logic: "AND"},
		{Field: "status", Value: "active"},
	})
	if err != nil {
		t.Fatalf("buildWhereClause 错误: %v", err)
	}
	wantClause := `WHERE "name" LIKE ? AND "status" = ?`
	if clause != wantClause {
		t.Errorf("WHERE 子句不匹配: %s", clause)
	}
	wantArgs := []interface{}{"%ohn%", "active"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("参数不匹配: %#v", args)
	}
}

// -----------------------------------------------------------------------------
// getTablesSet / detectTable / listColumns
// -----------------------------------------------------------------------------

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", "file:memdb1?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}

	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, status TEXT);`,
		`CREATE TABLE orders (order_id INTEGER PRIMARY KEY, amount INTEGER);`,
		`CREATE TABLE "` + innerPrefix + `sys_config" (k TEXT, v TEXT);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("初始化表失败: %v", err)
		}
	}
	return db
}

func TestGetTablesSetAndDetectTable(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	set, err := getTablesSet(db)
	if err != nil {
		t.Fatalf("getTablesSet 错误: %v", err)
	}
	if len(set) != 2 || set["users"] == struct{}{} == false || set["orders"] == struct{}{} == false {
		t.Errorf("结果集不正确: %#v", set)
	}

	first, err := detectTable(db)
	if err != nil {
		t.Fatalf("detectTable 错误: %v", err)
	}
	if first != "orders" { // 按字母序，orders < users
		t.Errorf("detectTable 结果错误, got=%s", first)
	}
}

func TestListColumns(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	cols, err := listColumns(db, "users")
	if err != nil {
		t.Fatalf("listColumns 错误: %v", err)
	}
	want := []string{"id", "name", "status"}
	if !reflect.DeepEqual(cols, want) {
		t.Errorf("列信息不匹配\n  got : %#v\n  want: %#v", cols, want)
	}
}
