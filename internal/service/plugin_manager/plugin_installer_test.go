// file: internal/service/plugin_installer_test.go
package plugin_manager

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"archive/zip"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestInstall_OK(t *testing.T) {
	tmpDir := t.TempDir()

	db, err := sql.Open("sqlite", filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("打开 sqlite 失败: %v", err)
	}
	defer db.Close()

	if err := createSchemaForTest(db); err != nil {
		t.Fatalf("创建测试表结构失败: %v", err)
	}

	pm, _ := NewPluginManager(
		db,
		tmpDir,
		nil,
		filepath.Join(tmpDir, "plugins"),
		map[string]port.DataSource{},
		&[]io.Closer{},
	)

	zipPath := filepath.Join(tmpDir, "demo-1.0.0.zip")
	if err := createDummyZip(zipPath); err != nil {
		t.Fatalf("创建临时 ZIP 失败: %v", err)
	}

	pv := domain.PluginVersion{
		VersionString: "1.0.0",
		Source:        domain.Source{URL: zipPath},
		Execution:     domain.Execution{Entrypoint: "dummy.sh"},
	}
	pm.catalog["demo"] = domain.PluginManifest{
		ID: "demo", Name: "演示插件", Versions: []domain.PluginVersion{pv},
	}

	if err := pm.Install("demo", "1.0.0"); err != nil {
		t.Fatalf("Install 期望成功，实际失败: %v", err)
	}
}

func createSchemaForTest(db *sql.DB) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS installed_plugins (
		plugin_id    TEXT NOT NULL,
		version      TEXT NOT NULL,
		install_path TEXT NOT NULL,
		PRIMARY KEY (plugin_id, version)
	);

	CREATE TABLE IF NOT EXISTS plugin_instances (
		instance_id      TEXT PRIMARY KEY,
		display_name     TEXT,
		plugin_id        TEXT,
		version          TEXT,
		biz_name         TEXT,
		port             INTEGER,
		status           TEXT,
		enabled          BOOLEAN,
		created_at       DATETIME,
		last_started_at  DATETIME
	);
	`
	_, err := db.Exec(ddl)
	return err
}

func createDummyZip(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := zip.NewWriter(f)
	_, err = w.Create("README.txt")
	if closeErr := w.Close(); err == nil {
		err = closeErr
	}
	return err
}
