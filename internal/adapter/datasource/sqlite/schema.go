// file: internal/adapter/datasource/sqlite/schema.go
package sqlite

import (
	"ArchiveAegis/internal/core/port"
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
	innerPrefix         = "_archiveaegis_internal_"
	schemaCacheFilename = "schema_cache.json"
)

// dbPhysicalSchemaInfo 存储从单个数据库文件探测到的物理结构信息。
type dbPhysicalSchemaInfo struct {
	detectedDefaultTable string
	allTablesAndColumns  map[string][]string
}

// schemaFile 表示写入磁盘的 schema_cache.json 的整体 JSON 结构
type schemaFile struct {
	UpdatedAt time.Time                      `json:"updated_at"`
	Tables    map[string][]string            `json:"tables"` // 并集，用于 /columns 时足够
	Libs      map[string]map[string][]string `json:"libs"`   // 每库各表列
}

// GetSchema 实现 port.DataSource 接口，返回由管理员配置定义的、可供查询的 Schema。
func (m *Manager) GetSchema(ctx context.Context, req port.SchemaRequest) (*port.SchemaResult, error) {
	bizConfig, err := m.configService.GetBizQueryConfig(ctx, req.BizName)
	if err != nil {
		return nil, fmt.Errorf("获取业务 '%s' 的 schema 配置失败: %w", req.BizName, err)
	}
	if bizConfig == nil {
		return nil, port.ErrBizNotFound
	}

	schemaTables := make(map[string][]port.FieldDescription)

	for tableName, tableConfig := range bizConfig.Tables {
		if req.TableName != "" && req.TableName != tableName {
			continue
		}

		// ✅ FIX: Changed port.Field_Description to port.FieldDescription
		var fields []port.FieldDescription
		for _, fieldSetting := range tableConfig.Fields {
			fields = append(fields, port.FieldDescription{
				Name:         fieldSetting.FieldName,
				DataType:     fieldSetting.DataType,
				IsSearchable: fieldSetting.IsSearchable,
				IsReturnable: fieldSetting.IsReturnable,
				IsPrimary:    false, // 暂未实现
				Description:  "",    // 暂未实现
			})
		}
		sort.Slice(fields, func(i, j int) bool {
			return fields[i].Name < fields[j].Name
		})
		schemaTables[tableName] = fields
	}

	if req.TableName != "" && len(schemaTables) == 0 {
		return nil, port.ErrTableNotFoundInBiz
	}

	return &port.SchemaResult{
		Tables: schemaTables,
	}, nil
}

// loadDBPhysicalSchema 从给定的数据库连接中加载其实际的物理表和列信息。
func loadDBPhysicalSchema(ctx context.Context, db *sql.DB) (*dbPhysicalSchemaInfo, error) {
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
func (m *Manager) loadOrRefreshSchemaInternal() {
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
func (m *Manager) loadOrRefreshSchema() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadOrRefreshSchemaInternal()
}

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
