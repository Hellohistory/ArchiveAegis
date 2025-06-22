// file: internal/adapter/datasource/sqlite/manager_schema.go
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// innerPrefix 定义了内部保留表的前缀，以避免被扫描
	innerPrefix = "_archiveaegis_internal_"

	// schemaCacheFilename 定义了物理 schema 缓存文件的名称
	schemaCacheFilename = "schema_cache.json"
)

// dbPhysicalSchemaInfo 存储从单个数据库文件探测到的物理结构信息。
type dbPhysicalSchemaInfo struct {
	detectedDefaultTable string
	allTablesAndColumns  map[string][]string // <--- 修复 'allTablesAndColumns' 未定义的引用
}

// schemaFile 表示写入磁盘的 schema_cache.json 的整体 JSON 结构
type schemaFile struct {
	UpdatedAt time.Time                      `json:"updated_at"`
	Tables    map[string][]string            `json:"tables"` // 并集，用于 /columns 时足够
	Libs      map[string]map[string][]string `json:"libs"`   // 每库各表列
}

// loadDBPhysicalSchema 从给定的数据库连接中加载其实际的物理表和列信息。
func loadDBPhysicalSchema(ctx context.Context, db *sql.DB) (*dbPhysicalSchemaInfo, error) { // <--- 修复 'loadDBPhysicalSchema' 未定义的引用
	autoDetectedDefaultTable, errDetect := detectTable(db)
	if errDetect != nil && errDetect != sql.ErrNoRows {
		log.Printf("警告: [DBManager] loadDBPhysicalSchema: 自动检测默认表失败: %v", errDetect)
	}
	if errDetect == sql.ErrNoRows {
		autoDetectedDefaultTable = ""
	}

	actualUserTables, errTables := getTablesSet(db)
	if errTables != nil {
		return nil, fmt.Errorf("loadDBPhysicalSchema: 获取物理表集合失败: %w", errTables)
	}

	allTablesAndPhysColumns := make(map[string][]string)
	if len(actualUserTables) > 0 {
		for tblName := range actualUserTables {
			physColumns, errCols := listColumns(db, tblName)
			if errCols != nil {
				log.Printf("警告: [DBManager] 表 '%s' 获取物理列信息失败: %v", tblName, errCols)
				allTablesAndPhysColumns[tblName] = []string{}
				continue
			}
			sort.Strings(physColumns)
			allTablesAndPhysColumns[tblName] = physColumns
		}
	}

	return &dbPhysicalSchemaInfo{
		detectedDefaultTable: autoDetectedDefaultTable,
		allTablesAndColumns:  allTablesAndPhysColumns,
	}, nil
}

// loadOrRefreshSchemaInternal 负责计算并更新 m.schema (业务组物理 Schema 并集缓存)。
// 调用此方法前必须获取写锁 m.mu.Lock()。
func (m *Manager) loadOrRefreshSchemaInternal() { // <--- 修复 'loadOrRefreshSchemaInternal' 未定义的引用
	log.Printf("信息: [DBManager] 开始刷新所有业务的 (物理) schema 并集缓存 (m.schema)...")
	newCombinedSchemaState := make(map[string]map[string][]string)

	for bizName, libsMapInBiz := range m.group {
		bizDirPath := filepath.Join(m.root, bizName)
		unionSchemaFromCache, _, errCache := readSchemaCache(bizDirPath)

		if errCache == nil && unionSchemaFromCache != nil {
			newCombinedSchemaState[bizName] = unionSchemaFromCache
			log.Printf("信息: [DBManager] 业务 '%s' 的物理 schema 并集已从缓存文件加载。", bizName)
		} else {
			if errCache != nil {
				log.Printf("警告: [DBManager] 业务 '%s' 读取 schema 缓存失败 (%v)，将执行全量扫描。", bizName, errCache)
			}
			currentBizSchemaUnion, currentBizPerLibSchema := m.computeSchemaUnionForBiz(bizName, libsMapInBiz)
			newCombinedSchemaState[bizName] = currentBizSchemaUnion
			if errWrite := writeSchemaCache(bizDirPath, currentBizPerLibSchema, currentBizSchemaUnion); errWrite != nil {
				log.Printf("错误: [DBManager] 业务 '%s' 写入 schema 缓存文件失败: %v", bizName, errWrite)
			} else {
				log.Printf("信息: [DBManager] 业务 '%s' 的 schema 并集已扫描并写入缓存。", bizName)
			}
		}
	}

	m.schema = newCombinedSchemaState
	log.Printf("信息: [DBManager] 所有业务的 schema 并集缓存 (m.schema) 刷新完成。")
}

// computeSchemaUnionForBiz 为单个业务组计算其下所有库的Schema并集。
func (m *Manager) computeSchemaUnionForBiz(bizName string, libsMapInBiz map[string]*sql.DB) (map[string][]string, map[string]map[string][]string) {
	union := make(map[string]map[string]struct{}) // tableName -> set of columnNames
	perLib := make(map[string]map[string][]string)

	for libName, dbConn := range libsMapInBiz {
		phySchema, found := m.dbSchemaCache[dbConn]
		if !found || phySchema == nil {
			log.Printf("错误: [DBManager] 业务 '%s' 库 '%s' 的物理 schema 未在缓存中找到。", bizName, libName)
			continue
		}
		perLib[libName] = phySchema.allTablesAndColumns
		for tableName, columns := range phySchema.allTablesAndColumns {
			if _, ok := union[tableName]; !ok {
				union[tableName] = make(map[string]struct{})
			}
			for _, col := range columns {
				union[tableName][col] = struct{}{}
			}
		}
	}

	// Convert set to sorted slice
	result := make(map[string][]string)
	for tableName, colSet := range union {
		cols := make([]string, 0, len(colSet))
		for col := range colSet {
			cols = append(cols, col)
		}
		sort.Strings(cols)
		result[tableName] = cols
	}
	return result, perLib
}

// loadOrRefreshSchema 是 loadOrRefreshSchemaInternal 的公开包装器，带锁。
func (m *Manager) loadOrRefreshSchema() { // <--- 修复 'loadOrRefreshSchema' 未定义的引用
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadOrRefreshSchemaInternal()
}

// --- 以下为从 helpers.go 移动过来的 Schema 相关辅助函数 ---

// getTablesSet 返回数据库中所有用户表的集合
func getTablesSet(db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE ?`, innerPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := make(map[string]struct{})
	for rows.Next() {
		var tbl string
		if err := rows.Scan(&tbl); err != nil {
			log.Printf("警告: [DBManager] getTablesSet 扫描表名失败: %v", err)
			continue
		}
		set[tbl] = struct{}{}
	}
	return set, rows.Err()
}

// detectTable 尝试检测数据库中的一个 "默认" 用户表
func detectTable(db *sql.DB) (string, error) {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE ? ORDER BY name ASC LIMIT 1`, innerPrefix+"%").Scan(&name)
	return name, err
}

// listColumns 返回指定表的所有物理列名
func listColumns(db *sql.DB, tableName string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, tableName))
	if err != nil {
		return nil, fmt.Errorf("PRAGMA table_info for table %q 失败: %w", tableName, err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var (
			cid       int
			colName   string
			colType   string
			notnull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &colName, &colType, &notnull, &dfltValue, &pk); err != nil {
			log.Printf("警告: [DBManager] listColumns for table '%s' 扫描列信息失败: %v", tableName, err)
			continue
		}
		cols = append(cols, colName)
	}
	return cols, rows.Err()
}

// --- 以下为新加入的 Schema 缓存读写函数 ---

// readSchemaCache 读取并反序列化 schema_cache.json。
func readSchemaCache(bizDir string) (map[string][]string, map[string]map[string][]string, error) {
	path := filepath.Join(bizDir, schemaCacheFilename)
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

// writeSchemaCache 覆盖写入 schema_cache.json。
func writeSchemaCache(bizDir string, libs map[string]map[string][]string, tables map[string][]string) error {
	tmp := filepath.Join(bizDir, schemaCacheFilename+".tmp")
	final := filepath.Join(bizDir, schemaCacheFilename)

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
