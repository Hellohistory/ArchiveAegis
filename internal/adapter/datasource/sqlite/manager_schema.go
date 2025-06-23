// Package sqlite file: internal/adapter/datasource/sqlite/manager_schema.go
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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
	allTablesAndColumns  map[string][]string
}

// schemaFile 表示写入磁盘的 schema_cache.json 的整体 JSON 结构
type schemaFile struct {
	UpdatedAt time.Time                      `json:"updated_at"`
	Tables    map[string][]string            `json:"tables"`
	Libs      map[string]map[string][]string `json:"libs"`
}

func (m *Manager) handleGetSchema(ctx context.Context, req *v1.RequestEnvelope) (proto.Message, error) {
	var schemaReq v1.GetSchemaRequest
	if err := req.Payload.UnmarshalTo(&schemaReq); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "解包 GetSchemaRequest 失败: %v", err)
	}

	result, err := m.getSchemaInternal(ctx, port.SchemaRequest{
		BizName:   req.BizName,
		TableName: schemaReq.TableName,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "获取 Schema 失败: %v", err)
	}

	grpcTables := make(map[string]*v1.TableSchema)
	for tableName, tableSchema := range result.Tables {
		var grpcFields []*v1.FieldDescription
		for _, field := range tableSchema {
			grpcFields = append(grpcFields, &v1.FieldDescription{
				Name:         field.Name,
				DataType:     field.DataType,
				IsSearchable: field.IsSearchable,
				IsReturnable: field.IsReturnable,
				IsPrimary:    field.IsPrimary,
				Description:  field.Description,
			})
		}
		grpcTables[tableName] = &v1.TableSchema{Fields: grpcFields}
	}

	return &v1.SchemaResult{Tables: grpcTables}, nil
}

func (m *Manager) getSchemaInternal(ctx context.Context, req port.SchemaRequest) (*port.SchemaResult, error) {
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
		var fields []port.FieldDescription
		for _, fieldSetting := range tableConfig.Fields {
			fields = append(fields, port.FieldDescription{
				Name:         fieldSetting.FieldName,
				DataType:     fieldSetting.DataType,
				IsSearchable: fieldSetting.IsSearchable,
				IsReturnable: fieldSetting.IsReturnable,
				IsPrimary:    false,
				Description:  "",
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

	return &port.SchemaResult{Tables: schemaTables}, nil
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
	union := make(map[string]map[string]struct{})
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
