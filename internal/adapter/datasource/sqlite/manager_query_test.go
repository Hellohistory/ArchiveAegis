// file: internal/adapter/datasource/sqlite/manager_query_test.go
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	_ "modernc.org/sqlite"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

// mockAdminConfigService 是 port.QueryAdminConfigService 接口的一个灵活的模拟实现。
// 允许在每个测试用例中按需设置不同的业务配置。
type mockAdminConfigService struct {
	configs map[string]*domain.BizQueryConfig
}

// newMockAdminConfigService 创建一个新的可配置的模拟服务。
func newMockAdminConfigService() *mockAdminConfigService {
	return &mockAdminConfigService{
		configs: make(map[string]*domain.BizQueryConfig),
	}
}

// SetBizConfig 为特定的业务名称设置查询配置。
func (m *mockAdminConfigService) SetBizConfig(bizName string, config *domain.BizQueryConfig) {
	m.configs[bizName] = config
}

// GetBizQueryConfig 实现了接口方法，返回预设的配置。
func (m *mockAdminConfigService) GetBizQueryConfig(ctx context.Context, bizName string) (*domain.BizQueryConfig, error) {
	config, ok := m.configs[bizName]
	if !ok {
		// 模拟业务未找到的情况
		return nil, port.ErrBizNotFound
	}
	return config, nil
}

// GetDefaultBizConfig 返回一个通用的、权限全开的默认配置，方便测试。
func GetDefaultBizConfig(bizName string) *domain.BizQueryConfig {
	return &domain.BizQueryConfig{
		BizName:              bizName,
		IsPubliclySearchable: true,
		DefaultQueryTable:    "test_table",
		Tables: map[string]*domain.TableConfig{
			"test_table": {
				TableName:    "test_table",
				IsSearchable: true,
				Fields: map[string]domain.FieldSetting{
					"id":      {FieldName: "id", IsReturnable: true, IsSearchable: true, DataType: "INTEGER"},
					"name":    {FieldName: "name", IsReturnable: true, IsSearchable: true, DataType: "TEXT"},
					"email":   {FieldName: "email", IsReturnable: true, IsSearchable: true, DataType: "TEXT"},
					"secret":  {FieldName: "secret", IsReturnable: false, IsSearchable: false, DataType: "TEXT"}, // 不可返回、不可搜索字段
					"dept_id": {FieldName: "dept_id", IsReturnable: true, IsSearchable: true, DataType: "INTEGER"},
				},
			},
			"unsearchable_table": {
				TableName:    "unsearchable_table",
				IsSearchable: false, // 设置为不可搜索
				Fields: map[string]domain.FieldSetting{
					"id": {FieldName: "id", IsReturnable: true, IsSearchable: true},
				},
			},
		},
	}
}

// 为满足接口要求，实现其他空方法
func (m *mockAdminConfigService) GetAllConfiguredBizNames(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateBizOverallSettings(ctx context.Context, bizName string, settings domain.BizOverallSettings) error {
	return nil
}
func (m *mockAdminConfigService) UpdateBizSearchableTables(ctx context.Context, bizName string, tableNames []string) error {
	return nil
}
func (m *mockAdminConfigService) UpdateTableWritePermissions(ctx context.Context, bizName, tableName string, perms domain.TableConfig) error {
	return nil
}
func (m *mockAdminConfigService) UpdateTableFieldSettings(ctx context.Context, bizName, tableName string, fields []domain.FieldSetting) error {
	return nil
}
func (m *mockAdminConfigService) GetDefaultViewConfig(ctx context.Context, bizName, tableName string) (*domain.ViewConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) GetAllViewConfigsForBiz(ctx context.Context, bizName string) (map[string][]*domain.ViewConfig, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateAllViewsForBiz(ctx context.Context, bizName string, viewsData map[string][]*domain.ViewConfig) error {
	return nil
}
func (m *mockAdminConfigService) GetIPLimitSettings(ctx context.Context) (*domain.IPLimitSetting, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateIPLimitSettings(ctx context.Context, settings domain.IPLimitSetting) error {
	return nil
}
func (m *mockAdminConfigService) GetUserLimitSettings(ctx context.Context, userID int64) (*domain.UserLimitSetting, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateUserLimitSettings(ctx context.Context, userID int64, settings domain.UserLimitSetting) error {
	return nil
}
func (m *mockAdminConfigService) GetBizRateLimitSettings(ctx context.Context, bizName string) (*domain.BizRateLimitSetting, error) {
	return nil, nil
}
func (m *mockAdminConfigService) UpdateBizRateLimitSettings(ctx context.Context, bizName string, settings domain.BizRateLimitSetting) error {
	return nil
}
func (m *mockAdminConfigService) InvalidateCacheForBiz(bizName string) {}
func (m *mockAdminConfigService) InvalidateAllCaches()                 {}

// setupInfo 定义了创建测试数据库所需的信息。
type setupInfo struct {
	LibName string   // 数据库文件名 (不含扩展名)
	Schema  string   // 建表语句
	Inserts []string // 插入数据语句
}

// setupTestManager 根据提供的 schema 和数据，创建并初始化一个用于测试的 Manager。
func setupTestManager(t *testing.T, bizName string, libs []setupInfo) (*Manager, func()) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "aegis_test_")
	if err != nil {
		t.Fatalf("无法创建临时目录: %v", err)
	}

	bizDir := filepath.Join(tempDir, bizName)
	if err := os.Mkdir(bizDir, 0755); err != nil {
		t.Fatalf("无法创建业务目录: %v", err)
	}

	for _, lib := range libs {
		dbPath := filepath.Join(bizDir, lib.LibName+".db")
		db, err := sql.Open("sqlite", "file:"+dbPath)
		if err != nil {
			t.Fatalf("无法打开数据库 '%s': %v", dbPath, err)
		}

		if _, err = db.Exec(lib.Schema); err != nil {
			db.Close()
			t.Fatalf("无法在 '%s' 中创建表: %v", dbPath, err)
		}
		for _, insert := range lib.Inserts {
			if _, err = db.Exec(insert); err != nil {
				db.Close()
				t.Fatalf("无法在 '%s' 中插入数据: %v", dbPath, err)
			}
		}
		db.Close()
	}

	mockCfgSvc := newMockAdminConfigService()
	manager := NewManager(mockCfgSvc)
	manager.configService = mockCfgSvc

	if err := manager.InitForBiz(context.Background(), tempDir, bizName); err != nil {
		t.Fatalf("InitForBiz 失败: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return manager, cleanup
}

func TestHandleDataQuery(t *testing.T) {
	// --- 测试场景 1: 成功查询 ---
	t.Run("成功场景", func(t *testing.T) {
		const bizName = "test_biz"
		libs := []setupInfo{
			{
				LibName: "lib1",
				Schema:  `CREATE TABLE test_table (id INTEGER PRIMARY KEY, name TEXT, email TEXT, secret TEXT, dept_id INT);`,
				Inserts: []string{
					`INSERT INTO test_table (id, name, email, secret, dept_id) VALUES (1, 'user_a', 'a@example.com', 'secret_a', 101)`,
					`INSERT INTO test_table (id, name, email, secret, dept_id) VALUES (2, 'user_b', 'b@example.com', 'secret_b', 102)`,
				},
			},
			{
				LibName: "lib2",
				Schema:  `CREATE TABLE test_table (id INTEGER PRIMARY KEY, name TEXT, email TEXT, secret TEXT, dept_id INT);`,
				Inserts: []string{
					`INSERT INTO test_table (id, name, email, secret, dept_id) VALUES (3, 'user_c', 'c@example.com', 'secret_c', 101)`,
				},
			},
			{
				LibName: "lib3_no_table", // 这个库里故意不创建 test_table
				Schema:  `CREATE TABLE other_table (id INTEGER);`,
			},
		}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()
		// 为 manager 设置默认的 permissive 配置
		mockCfg := manager.configService.(*mockAdminConfigService)
		mockCfg.SetBizConfig(bizName, GetDefaultBizConfig(bizName))

		testCases := []struct {
			name           string
			query          map[string]interface{}
			expectedTotal  int64
			expectedItems  []map[string]interface{}
			expectedErrMsg string
		}{
			{
				name:          "基础查询(跨库)",
				query:         map[string]interface{}{"table": "test_table"},
				expectedTotal: 3,
				expectedItems: []map[string]interface{}{
					{"__lib": "lib1", "id": float64(1), "name": "user_a", "email": "a@example.com", "dept_id": float64(101)},
					{"__lib": "lib1", "id": float64(2), "name": "user_b", "email": "b@example.com", "dept_id": float64(102)},
					{"__lib": "lib2", "id": float64(3), "name": "user_c", "email": "c@example.com", "dept_id": float64(101)},
				},
			},
			{
				name: "带单一精确筛选",
				query: map[string]interface{}{
					"table": "test_table",
					"filters": []interface{}{
						map[string]interface{}{"field": "name", "value": "user_b"},
					},
				},
				expectedTotal: 1,
				expectedItems: []map[string]interface{}{
					{"__lib": "lib1", "id": float64(2), "name": "user_b", "email": "b@example.com", "dept_id": float64(102)},
				},
			},
			{
				name: "带多重筛选(AND)",
				query: map[string]interface{}{
					"table": "test_table",
					"filters": []interface{}{
						map[string]interface{}{"field": "dept_id", "value": "101"},
						map[string]interface{}{"field": "name", "value": "user_a"},
					},
				},
				expectedTotal: 1,
				expectedItems: []map[string]interface{}{
					{"__lib": "lib1", "id": float64(1), "name": "user_a", "email": "a@example.com", "dept_id": float64(101)},
				},
			},
			{
				name: "指定返回字段",
				query: map[string]interface{}{
					"table":            "test_table",
					"fields_to_return": []interface{}{"id", "name"},
					"filters": []interface{}{
						map[string]interface{}{"field": "name", "value": "user_c"},
					},
				},
				expectedTotal: 1,
				expectedItems: []map[string]interface{}{
					{"__lib": "lib2", "id": float64(3), "name": "user_c"},
				},
			},
			{
				name: "模糊查询",
				query: map[string]interface{}{
					"table": "test_table",
					"filters": []interface{}{
						map[string]interface{}{"field": "email", "value": "@example.com", "fuzzy": true},
					},
				},
				expectedTotal: 3,
				expectedItems: []map[string]interface{}{
					{"__lib": "lib1", "id": float64(1), "name": "user_a", "email": "a@example.com", "dept_id": float64(101)},
					{"__lib": "lib1", "id": float64(2), "name": "user_b", "email": "b@example.com", "dept_id": float64(102)},
					{"__lib": "lib2", "id": float64(3), "name": "user_c", "email": "c@example.com", "dept_id": float64(101)},
				},
			},
			{
				name: "分页查询",
				query: map[string]interface{}{
					"table": "test_table",
					"page":  2,
					"size":  2,
				},
				expectedTotal: 3,
				expectedItems: []map[string]interface{}{
					{"__lib": "lib2", "id": float64(3), "name": "user_c", "email": "c@example.com", "dept_id": float64(101)},
				},
			},
			{
				name:          "查询无结果",
				query:         map[string]interface{}{"table": "test_table", "filters": []interface{}{map[string]interface{}{"field": "name", "value": "non_existent_user"}}},
				expectedTotal: 0,
				expectedItems: []map[string]interface{}{},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				queryPayload, _ := structpb.NewStruct(tc.query)
				reqPayload := &v1.DataQueryRequest{Query: queryPayload}
				packedPayload, _ := anypb.New(reqPayload)
				reqEnvelope := &v1.RequestEnvelope{BizName: bizName, Payload: packedPayload}

				resPayload, err := manager.handleDataQuery(context.Background(), reqEnvelope)

				if tc.expectedErrMsg != "" {
					if err == nil || err.Error() != tc.expectedErrMsg {
						t.Fatalf("期望错误 '%s', 实际得到: %v", tc.expectedErrMsg, err)
					}
					return
				}
				if err != nil {
					t.Fatalf("期望没有错误, 但得到: %v", err)
				}

				queryResult, ok := resPayload.(*v1.DataQueryResult)
				if !ok {
					t.Fatalf("响应载荷类型不是 *DataQueryResult, 而是 %T", resPayload)
				}

				resultMap := queryResult.Data.AsMap()
				total, _ := resultMap["total"].(float64)
				if int64(total) != tc.expectedTotal {
					t.Errorf("期望 total 为 %d, 但得到 %v", tc.expectedTotal, total)
				}

				items, _ := resultMap["items"].([]interface{})

				// 为了比较，将结果转换为可比较的 map，并排序
				gotItems := make([]map[string]interface{}, len(items))
				for i, item := range items {
					gotItems[i] = item.(map[string]interface{})
				}

				// 排序以确保比较的稳定性 (按 id 排序)
				sort.SliceStable(gotItems, func(i, j int) bool {
					idI, _ := gotItems[i]["id"].(float64)
					idJ, _ := gotItems[j]["id"].(float64)
					return idI < idJ
				})
				sort.SliceStable(tc.expectedItems, func(i, j int) bool {
					idI, _ := tc.expectedItems[i]["id"].(float64)
					idJ, _ := tc.expectedItems[j]["id"].(float64)
					return idI < idJ
				})

				if !reflect.DeepEqual(gotItems, tc.expectedItems) {
					t.Errorf("期望的 items 是:\n%#v\n但得到:\n%#v", tc.expectedItems, gotItems)
				}

				if len(items) != len(tc.expectedItems) {
					t.Fatalf("期望 items 长度为 %d, 但得到 %d", len(tc.expectedItems), len(items))
				}
			})
		}
	})

	// --- 测试场景 2: 权限和配置错误 ---
	t.Run("权限和配置错误", func(t *testing.T) {
		const bizName = "perm_test_biz"
		libs := []setupInfo{
			{LibName: "lib1", Schema: `CREATE TABLE test_table (id INT, name TEXT, secret TEXT); CREATE TABLE unsearchable_table (id INT);`},
		}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()

		testCases := []struct {
			name           string
			bizConfig      *domain.BizQueryConfig // 使用指针，nil 表示不设置配置
			query          map[string]interface{}
			expectedCode   codes.Code
			expectedErrMsg string
		}{
			{
				name:           "业务不存在",
				bizConfig:      nil,
				query:          map[string]interface{}{"table": "test_table"},
				expectedCode:   codes.Internal,
				expectedErrMsg: "查询数据失败: 业务 'perm_test_biz' 查询配置不可用: 指定的业务组未找到",
			},
			{
				name: "业务设置为不可搜索",
				bizConfig: func() *domain.BizQueryConfig {
					cfg := GetDefaultBizConfig(bizName)
					cfg.IsPubliclySearchable = false
					return cfg
				}(),
				query:          map[string]interface{}{"table": "test_table"},
				expectedCode:   codes.Internal,
				expectedErrMsg: "查询数据失败: 权限不足，操作被拒绝",
			},
			{
				name:           "表在配置中不存在",
				bizConfig:      GetDefaultBizConfig(bizName),
				query:          map[string]interface{}{"table": "non_existent_table"},
				expectedCode:   codes.Internal,
				expectedErrMsg: "查询数据失败: 在当前业务组的配置中未找到指定的表",
			},
			{
				name:           "表设置为不可搜索",
				bizConfig:      GetDefaultBizConfig(bizName),
				query:          map[string]interface{}{"table": "unsearchable_table"},
				expectedCode:   codes.Internal,
				expectedErrMsg: "查询数据失败: 权限不足，操作被拒绝",
			},
			{
				name:      "筛选不可搜索的字段",
				bizConfig: GetDefaultBizConfig(bizName),
				query: map[string]interface{}{
					"table":   "test_table",
					"filters": []interface{}{map[string]interface{}{"field": "secret", "value": "any"}},
				},
				expectedCode:   codes.Internal,
				expectedErrMsg: "查询数据失败: 字段 'secret' 无效或不可搜索",
			},
			{
				name:      "请求不可返回的字段",
				bizConfig: GetDefaultBizConfig(bizName),
				query: map[string]interface{}{
					"table":            "test_table",
					"fields_to_return": []interface{}{"id", "secret"},
				},
				expectedCode:   codes.Internal,
				expectedErrMsg: "查询数据失败: 安全策略冲突：字段 'secret' 未被授权返回",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				mockCfg := manager.configService.(*mockAdminConfigService)
				mockCfg.configs = make(map[string]*domain.BizQueryConfig)
				if tc.bizConfig != nil {
					mockCfg.SetBizConfig(bizName, tc.bizConfig)
				}

				queryPayload, _ := structpb.NewStruct(tc.query)
				reqPayload := &v1.DataQueryRequest{Query: queryPayload}
				packedPayload, _ := anypb.New(reqPayload)
				reqEnvelope := &v1.RequestEnvelope{BizName: bizName, Payload: packedPayload}

				_, err := manager.handleDataQuery(context.Background(), reqEnvelope)

				if err == nil {
					t.Fatalf("期望得到错误, 但实际为 nil")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("期望得到 gRPC status 错误, 实际为 %T: %v", err, err)
				}
				if st.Code() != tc.expectedCode {
					t.Errorf("期望错误码 %s, 但得到 %s", tc.expectedCode, st.Code())
				}
				if st.Message() != tc.expectedErrMsg {
					t.Errorf("期望错误信息 '%s', 但得到 '%s'", tc.expectedErrMsg, st.Message())
				}
			})
		}
	})

	// --- 测试场景 3: 无效输入 ---
	t.Run("无效输入", func(t *testing.T) {
		manager, cleanup := setupTestManager(t, "invalid_input_biz", []setupInfo{})
		defer cleanup()

		testCases := []struct {
			name           string
			query          map[string]interface{}
			expectedCode   codes.Code
			expectedErrMsg string
		}{
			{
				name:           "请求体中缺少 table 字段",
				query:          map[string]interface{}{"some_other_field": "value"},
				expectedCode:   codes.InvalidArgument,
				expectedErrMsg: "无效请求: query 体必须包含一个有效的 'table' 字符串字段",
			},
			{
				name:           "table 字段不是字符串",
				query:          map[string]interface{}{"table": 123},
				expectedCode:   codes.InvalidArgument,
				expectedErrMsg: "无效请求: query 体必须包含一个有效的 'table' 字符串字段",
			},
			{
				name: "filters 数组中的元素不是对象",
				query: map[string]interface{}{
					"table":   "test_table",
					"filters": []interface{}{"not_an_object"},
				},
				expectedCode:   codes.InvalidArgument,
				expectedErrMsg: "无效请求: filters 数组的第 0 个元素不是一个有效的JSON对象",
			},
			{
				name: "filter 对象缺少 field 字段",
				query: map[string]interface{}{
					"table":   "test_table",
					"filters": []interface{}{map[string]interface{}{"value": "some_value"}},
				},
				expectedCode:   codes.InvalidArgument,
				expectedErrMsg: "无效请求: filter 对象缺少或 'field' 字段类型不正确",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				queryPayload, _ := structpb.NewStruct(tc.query)
				reqPayload := &v1.DataQueryRequest{Query: queryPayload}
				packedPayload, _ := anypb.New(reqPayload)
				reqEnvelope := &v1.RequestEnvelope{BizName: "invalid_input_biz", Payload: packedPayload}

				_, err := manager.handleDataQuery(context.Background(), reqEnvelope)
				if err == nil {
					t.Fatal("期望有错误, 但结果为 nil")
				}
				st, ok := status.FromError(err)
				if !ok {
					t.Fatalf("期望 gRPC 错误, 得到 %T", err)
				}
				if st.Code() != tc.expectedCode {
					t.Errorf("期望错误码 %s, 得到 %s", tc.expectedCode, st.Code())
				}
				if st.Message() != tc.expectedErrMsg {
					t.Errorf("期望错误信息:\n%s\n实际得到:\n%s", tc.expectedErrMsg, st.Message())
				}
			})
		}
	})
}

// TestHandleDataQuery_OriginalSuccessCase 保留原始的成功用例作为基线检查。
func TestHandleDataQuery_OriginalSuccessCase(t *testing.T) {
	const bizName = "test_biz_original"
	libs := []setupInfo{
		{
			LibName: "test_lib",
			Schema:  `CREATE TABLE test_table (id INTEGER PRIMARY KEY, name TEXT, email TEXT, secret TEXT, dept_id INT);`,
			Inserts: []string{`INSERT INTO test_table (id, name) VALUES (1, 'aegis_user')`},
		},
	}
	manager, cleanup := setupTestManager(t, bizName, libs)
	defer cleanup()
	mockCfg := manager.configService.(*mockAdminConfigService)
	mockCfg.SetBizConfig(bizName, GetDefaultBizConfig(bizName))

	queryPayload, _ := structpb.NewStruct(map[string]interface{}{
		"table": "test_table",
	})
	reqPayload := &v1.DataQueryRequest{Query: queryPayload}
	packedPayload, _ := anypb.New(reqPayload)

	reqEnvelope := &v1.RequestEnvelope{
		BizName: bizName,
		Payload: packedPayload,
	}

	resPayload, err := manager.handleDataQuery(context.Background(), reqEnvelope)

	if err != nil {
		t.Fatalf("handleDataQuery 期望没有错误，但得到: %v", err)
	}

	queryResult, ok := resPayload.(*v1.DataQueryResult)
	if !ok {
		t.Fatalf("响应载荷类型不是 *DataQueryResult，而是 %T", resPayload)
	}

	resultMap := queryResult.Data.AsMap()
	total, _ := resultMap["total"].(float64)

	if int(total) != 1 {
		t.Errorf("期望 total 为 1, 但得到 %v", total)
	}

	items, ok := resultMap["items"].([]interface{})
	if !ok {
		t.Fatalf("结果中的 items 不是一个切片")
	}

	if len(items) != 1 {
		t.Fatalf("期望 items 长度为 1, 但得到 %d", len(items))
	}

	firstItem, ok := items[0].(map[string]interface{})
	if !ok {
		t.Fatalf("第一个 item 不是一个 map")
	}

	// 自动返回所有可返回字段
	expectedItem := map[string]interface{}{
		"__lib":   "test_lib",
		"id":      float64(1),
		"name":    "aegis_user",
		"email":   nil, // 在数据库中为 NULL
		"dept_id": nil, // 在数据库中为 NULL
	}

	// 为了稳定比较，对 map 的 key 进行排序后比较
	var gotKeys, expectedKeys []string
	for k := range firstItem {
		gotKeys = append(gotKeys, k)
	}
	for k := range expectedItem {
		expectedKeys = append(expectedKeys, k)
	}
	sort.Strings(gotKeys)
	sort.Strings(expectedKeys)
	if !reflect.DeepEqual(gotKeys, expectedKeys) {
		t.Errorf("期望的 item keys 是 %v, 但得到 %v", expectedKeys, gotKeys)
	}

	// 逐个字段比较
	for _, k := range expectedKeys {
		if fmt.Sprintf("%v", firstItem[k]) != fmt.Sprintf("%v", expectedItem[k]) {
			t.Errorf("字段 '%s' 不匹配: 期望 %v (类型 %T), 得到 %v (类型 %T)", k, expectedItem[k], expectedItem[k], firstItem[k], firstItem[k])
		}
	}
}
