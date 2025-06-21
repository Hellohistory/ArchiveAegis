// file: internal/adapter/datasource/sqlite/manager_test.go

package sqlite

import (
	"database/sql"
	"reflect"
	"testing"

	_ "modernc.org/sqlite"
)

// -----------------------------------------------------------------------------
// 测试辅助: 内存 SQLite 连接 & Dummy ConfigService
// -----------------------------------------------------------------------------

// newMemoryDB 返回一个共享内存 SQLite 连接，便于单测快速构造。
func newMemoryDB(t *testing.T, name string) *sql.DB {
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("无法打开内存数据库 %s: %v", name, err)
	}
	// 简单建表，便于将来扩展测试
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS dummy(id INTEGER);`); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return db
}

// -----------------------------------------------------------------------------
// Test: Type()
// -----------------------------------------------------------------------------

func TestManager_Type(t *testing.T) {
	m := &Manager{}
	if got := m.Type(); got != "sqlite_builtin" {
		t.Errorf("Type() 返回值错误, want=sqlite_builtin, got=%s", got)
	}
}

// -----------------------------------------------------------------------------
// Test: Summary()
// -----------------------------------------------------------------------------

func TestManager_Summary(t *testing.T) {
	m := &Manager{
		group: map[string]map[string]*sql.DB{
			"supply": {
				"a.db": newMemoryDB(t, "a1"),
				"b.db": newMemoryDB(t, "b1"),
			},
			"sales": {
				"c.db": newMemoryDB(t, "c1"),
			},
		},
	}

	got := m.Summary()

	// 期望业务组数量
	if len(got) != 2 {
		t.Fatalf("Summary() 返回业务组数量错误, want=2, got=%d", len(got))
	}

	// 业务组内库名应按字母序排序
	wantSupply := []string{"a.db", "b.db"}
	if !reflect.DeepEqual(got["supply"], wantSupply) {
		t.Errorf("supply 组库名排序/内容错误\n  got : %#v\n  want: %#v", got["supply"], wantSupply)
	}

	// sales 组只有一个库
	if len(got["sales"]) != 1 || got["sales"][0] != "c.db" {
		t.Errorf("sales 组结果错误, got=%#v", got["sales"])
	}
}

// -----------------------------------------------------------------------------
// Test: Close()
// -----------------------------------------------------------------------------

func TestManager_Close(t *testing.T) {
	// 准备 Manager，注入两个打开的连接
	db1 := newMemoryDB(t, "close1")
	db2 := newMemoryDB(t, "close2")

	m := &Manager{
		group: map[string]map[string]*sql.DB{
			"biz": {
				"x.db": db1,
				"y.db": db2,
			},
		},
		dbSchemaCache: make(map[*sql.DB]*dbPhysicalSchemaInfo),
	}

	// 调用 Close
	if err := m.Close(); err != nil {
		t.Errorf("Close() 返回错误: %v", err)
	}

	// (1) group / dbSchemaCache 应清空
	if len(m.group) != 0 || len(m.dbSchemaCache) != 0 {
		t.Error("Close() 未清空内部状态")
	}

	// (2) 数据库连接应真正关闭
	if err := db1.Ping(); err == nil {
		t.Error("db1 仍可 Ping, 未被关闭")
	}
	if err := db2.Ping(); err == nil {
		t.Error("db2 仍可 Ping, 未被关闭")
	}
}
