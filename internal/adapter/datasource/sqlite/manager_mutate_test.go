// file: internal/adapter/datasource/sqlite/manager_mutate_test.go
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/domain"
	"context"
	"database/sql"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

// getDBForTest 根据测试上下文、Manager 实例及业务和库名称重新打开对应的 SQLite 数据库并返回 *sql.DB 对象。
// t: 测试上下文；manager: 待测试的 Manager 实例；bizName: 业务标识；libName: 库标识。
func getDBForTest(t *testing.T, manager *Manager, bizName, libName string) *sql.DB {
	t.Helper()
	bizGroup := manager.group[bizName]
	if bizGroup == nil {
		t.Fatalf("测试设置错误：找不到业务组 '%s'", bizName)
	}
	instance, ok := bizGroup[libName]
	if !ok {
		t.Fatalf("测试设置错误：找不到库 '%s'", libName)
	}
	db, err := sql.Open("sqlite", instance.path)
	if err != nil {
		t.Fatalf("无法为断言重新打开数据库: %v", err)
	}
	return db
}

// TestHandleDataMutate 测试 Manager.handleDataMutate 方法在不同写操作场景下的行为，包括创建、更新、删除及权限校验。
func TestHandleDataMutate(t *testing.T) {
	const bizName = "mutate_biz"
	const libName = "lib1"

	// 准备一个具有全部写操作权限的业务配置模板
	fullPermissionConfig := GetDefaultBizConfig(bizName)
	fullPermissionConfig.Tables["test_mutate"] = &domain.TableConfig{
		TableName:    "test_mutate",
		IsSearchable: true,
		AllowCreate:  true,
		AllowUpdate:  true,
		AllowDelete:  true,
		Fields: map[string]domain.FieldSetting{
			"id":   {FieldName: "id", IsReturnable: true, IsSearchable: true},
			"name": {FieldName: "name", IsReturnable: true, IsSearchable: true},
			"age":  {FieldName: "age", IsReturnable: true, IsSearchable: true},
		},
	}

	t.Run("成功创建(Create)", func(t *testing.T) {
		// 并行执行子测试以提高测试效率
		t.Parallel()

		// 初始化数据库环境并注册业务配置
		libs := []setupInfo{{LibName: libName, Schema: `CREATE TABLE test_mutate (id INTEGER PRIMARY KEY, name TEXT, age INT);`}}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()
		mockCfgSvc := manager.configService.(*mockAdminConfigService)
		mockCfgSvc.SetBizConfig(bizName, fullPermissionConfig)

		// 构造创建操作的请求负载
		createPayload, _ := structpb.NewStruct(map[string]interface{}{
			"table_name": "test_mutate",
			"data":       map[string]interface{}{"id": 1, "name": "new_user", "age": 25},
		})
		reqPayload := &v1.DataMutateRequest{Operation: "create", Payload: createPayload}
		packedPayload, _ := anypb.New(reqPayload)
		reqEnvelope := &v1.RequestEnvelope{BizName: bizName, Payload: packedPayload}

		// 调用被测方法并验证结果
		resPayload, err := manager.handleDataMutate(context.Background(), reqEnvelope)

		if err != nil {
			t.Fatalf("期望没有错误, 但得到: %v", err)
		}
		mutateResult, _ := resPayload.(*v1.DataMutateResult)
		if rowsAffected, _ := mutateResult.Data.AsMap()["rows_affected"].(float64); int(rowsAffected) != 1 {
			t.Errorf("期望影响行数为 1, 得到 %v", rowsAffected)
		}

		// 验证数据库中是否已插入新记录
		db := getDBForTest(t, manager, bizName, libName)
		defer db.Close()
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM test_mutate WHERE id = 1 AND name = 'new_user'").Scan(&count)
		if err != nil {
			t.Fatalf("查询验证时出错: %v", err)
		}
		if count != 1 {
			t.Errorf("期望在数据库中找到新创建的记录, 但 count 为 %d", count)
		}
	})

	t.Run("成功更新(Update)", func(t *testing.T) {
		// 并行执行子测试
		t.Parallel()

		// 初始化数据库并插入初始记录
		libs := []setupInfo{{LibName: libName, Schema: `CREATE TABLE test_mutate (id INTEGER PRIMARY KEY, name TEXT, age INT);`, Inserts: []string{`INSERT INTO test_mutate (id, name, age) VALUES (1, 'user_one', 30)`}}}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()
		mockCfgSvc := manager.configService.(*mockAdminConfigService)
		mockCfgSvc.SetBizConfig(bizName, fullPermissionConfig)

		// 构造更新操作的请求负载
		updatePayload, _ := structpb.NewStruct(map[string]interface{}{
			"table_name": "test_mutate",
			"data":       map[string]interface{}{"age": 40},
			"filters":    []interface{}{map[string]interface{}{"field": "id", "value": 1}},
		})
		reqPayload := &v1.DataMutateRequest{Operation: "update", Payload: updatePayload}
		packedPayload, _ := anypb.New(reqPayload)
		reqEnvelope := &v1.RequestEnvelope{BizName: bizName, Payload: packedPayload}

		// 调用被测方法并验证更新结果
		_, err := manager.handleDataMutate(context.Background(), reqEnvelope)

		if err != nil {
			t.Fatalf("期望没有错误, 但得到: %v", err)
		}
		db := getDBForTest(t, manager, bizName, libName)
		defer db.Close()
		var age int
		err = db.QueryRow("SELECT age FROM test_mutate WHERE id = 1").Scan(&age)
		if err != nil {
			t.Fatalf("查询验证时出错: %v", err)
		}
		if age != 40 {
			t.Errorf("期望 age 更新为 40, 但得到 %d", age)
		}
	})

	t.Run("成功删除(Delete)", func(t *testing.T) {
		// 并行执行子测试
		t.Parallel()

		// 初始化数据库并插入待删除记录
		libs := []setupInfo{{LibName: libName, Schema: `CREATE TABLE test_mutate (id INTEGER PRIMARY KEY, name TEXT, age INT);`, Inserts: []string{`INSERT INTO test_mutate (id, name, age) VALUES (1, 'user_one', 30)`}}}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()
		mockCfgSvc := manager.configService.(*mockAdminConfigService)
		mockCfgSvc.SetBizConfig(bizName, fullPermissionConfig)

		// 构造删除操作的请求负载
		deletePayload, _ := structpb.NewStruct(map[string]interface{}{
			"table_name": "test_mutate",
			"filters":    []interface{}{map[string]interface{}{"field": "id", "value": 1}},
		})
		reqPayload := &v1.DataMutateRequest{Operation: "delete", Payload: deletePayload}
		packedPayload, _ := anypb.New(reqPayload)
		reqEnvelope := &v1.RequestEnvelope{BizName: bizName, Payload: packedPayload}

		// 调用被测方法并验证删除结果
		_, err := manager.handleDataMutate(context.Background(), reqEnvelope)

		if err != nil {
			t.Fatalf("期望没有错误, 但得到: %v", err)
		}
		db := getDBForTest(t, manager, bizName, libName)
		defer db.Close()
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM test_mutate WHERE id = 1").Scan(&count)
		if err != nil {
			t.Fatalf("查询验证时出错: %v", err)
		}
		if count != 0 {
			t.Errorf("期望记录被删除, 但 count 仍然为 %d", count)
		}
	})

	t.Run("权限错误(AllowCreate=false)", func(t *testing.T) {
		// 并行执行子测试
		t.Parallel()

		// 初始化数据库并设置禁止创建权限的业务配置
		libs := []setupInfo{{LibName: libName, Schema: `CREATE TABLE test_mutate (id INTEGER PRIMARY KEY, name TEXT, age INT);`}}
		manager, cleanup := setupTestManager(t, bizName, libs)
		defer cleanup()
		mockCfgSvc := manager.configService.(*mockAdminConfigService)

		permissionDeniedConfig := GetDefaultBizConfig(bizName)
		permissionDeniedConfig.Tables["test_mutate"] = &domain.TableConfig{
			TableName:   "test_mutate",
			AllowCreate: false,
			AllowUpdate: true,
			AllowDelete: true,
		}
		mockCfgSvc.SetBizConfig(bizName, permissionDeniedConfig)

		// 构造创建操作请求并验证权限拒绝
		createPayload, _ := structpb.NewStruct(map[string]interface{}{
			"table_name": "test_mutate",
			"data":       map[string]interface{}{"id": 3, "name": "user_three"},
		})
		reqPayload := &v1.DataMutateRequest{Operation: "create", Payload: createPayload}
		packedPayload, _ := anypb.New(reqPayload)
		reqEnvelope := &v1.RequestEnvelope{BizName: bizName, Payload: packedPayload}

		_, err := manager.handleDataMutate(context.Background(), reqEnvelope)

		if err == nil {
			t.Fatal("期望得到权限错误, 但实际为 nil")
		}
		st, ok := status.FromError(err)
		if !ok {
			t.Fatalf("期望得到 gRPC status 错误, 但得到 %T", err)
		}
		if st.Code() != codes.Internal || st.Message() != "写操作执行失败: 权限不足，操作被拒绝" {
			t.Errorf("期望的错误不匹配, 得到: code=%s, msg='%s'", st.Code(), st.Message())
		}
	})
}
