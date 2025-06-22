// file: internal/service/plugin_manager_test.go
package plugin_manager

import (
	"ArchiveAegis/internal/core/port"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNewPluginManager_Success(t *testing.T) {
	// 构造临时 sqlite 数据库
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("无法打开sqlite测试数据库: %v", err)
	}
	defer db.Close()

	installDir := filepath.Join(tmpDir, "plugins")

	registry := make(map[string]port.DataSource)
	closers := &[]io.Closer{}

	repos := []RepositoryConfig{
		{Name: "main", URL: "https://example.com/repo", Enabled: true},
	}

	pm, err := NewPluginManager(db, tmpDir, repos, installDir, registry, closers)
	if err != nil {
		t.Errorf("期望 NewPluginManager 正常返回, 实际: %v", err)
	}
	if pm == nil {
		t.Fatal("期望 PluginManager 实例不为 nil")
	}

	if pm.installDir != installDir {
		t.Errorf("installDir 初始化错误，期望: %s, 实际: %s", installDir, pm.installDir)
	}
	if len(pm.repositories) != 1 {
		t.Errorf("repositories 初始化数量错误")
	}
}

func TestNewPluginManager_NilDB(t *testing.T) {
	tmpDir := t.TempDir()
	repos := []RepositoryConfig{}
	registry := make(map[string]port.DataSource)
	closers := &[]io.Closer{}

	pm, err := NewPluginManager(nil, tmpDir, repos, tmpDir, registry, closers)
	if err == nil {
		t.Errorf("db为nil时应报错，实际无错")
	}
	if pm != nil {
		t.Errorf("db为nil时PluginManager应为nil，实际: %v", pm)
	}
}

func TestNewPluginManager_EmptyInstallDir(t *testing.T) {
	tmpDir := t.TempDir()
	db, _ := sql.Open("sqlite", ":memory:")
	defer db.Close()
	repos := []RepositoryConfig{}
	registry := make(map[string]port.DataSource)
	closers := &[]io.Closer{}

	pm, err := NewPluginManager(db, tmpDir, repos, "", registry, closers)
	if err == nil {
		t.Errorf("installDir为空时应报错，实际无错")
	}
	if pm != nil {
		t.Errorf("installDir为空时PluginManager应为nil，实际: %v", pm)
	}
}

func TestNewPluginManager_CreateInstallDirFail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("目录权限测试仅在类 Unix 系统下有效")
	}
	if os.Geteuid() == 0 {
		t.Skip("root 用户下跳过目录权限测试")
	}

	db, _ := sql.Open("sqlite", ":memory:")
	defer db.Close()
	repos := []RepositoryConfig{}
	registry := make(map[string]port.DataSource)
	closers := &[]io.Closer{}

	pm, err := NewPluginManager(db, "", repos, "/root/forbidden", registry, closers)
	if err == nil {
		t.Errorf("不能创建installDir时应报错，实际无错")
	}
	if pm != nil {
		t.Errorf("不能创建installDir时PluginManager应为nil，实际: %v", pm)
	}
}
