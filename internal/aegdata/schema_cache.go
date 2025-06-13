// Package aegdata — 负责 schema_cache.json 的持久化
package aegdata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

/*
================================================================
  schema_cache.json 结构体
================================================================
*/

// schemaFile 表示写入磁盘的整体 JSON 结构
type schemaFile struct {
	UpdatedAt time.Time                      `json:"updated_at"`
	Tables    map[string][]string            `json:"tables"` // 并集，用/columns 时够用
	Libs      map[string]map[string][]string `json:"libs"`   // 每库各表列
}

// readSchemaCache 读取并反序列化 schema_cache.json；文件不存在或解析失败都返回错误
func readSchemaCache(bizDir string) (map[string][]string, map[string]map[string][]string, error) {
	path := filepath.Join(bizDir, "schema_cache.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	var sf schemaFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, nil, err
	}
	return sf.Tables, sf.Libs, nil
}

// writeSchemaCache 覆盖写 schema_cache.json（先写 tmp 再 rename，避免半文件）
func writeSchemaCache(bizDir string, libs map[string]map[string][]string, tables map[string][]string) error {
	tmp := filepath.Join(bizDir, "schema_cache.json.tmp")
	final := filepath.Join(bizDir, "schema_cache.json")

	sf := schemaFile{
		UpdatedAt: time.Now().UTC(),
		Tables:    tables,
		Libs:      libs,
	}
	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
