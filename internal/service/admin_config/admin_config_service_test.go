// file: internal/service/admin_config/admin_config_service_test.go

package admin_config

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// newTestService 用于初始化测试服务与sqlmock
func newTestService(t *testing.T) (*AdminConfigServiceImpl, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("初始化sqlmock失败: %v", err)
	}
	svc, err := NewAdminConfigServiceImpl(db, 10, time.Minute)
	if err != nil {
		t.Fatalf("初始化AdminConfigServiceImpl失败: %v", err)
	}
	teardown := func() { db.Close() }
	return svc, mock, teardown
}

// ===============================
// 主流程测试（正常场景）
// ===============================
func TestLoadBizQueryConfigFromDB_Normal(t *testing.T) {
	svc, mock, teardown := newTestService(t)
	defer teardown()
	ctx := context.Background()

	// 1. Mock 总体配置
	rowsSetting := sqlmock.NewRows([]string{"is_publicly_searchable", "default_query_table"}).
		AddRow(true, "main")
	mock.ExpectQuery("SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings").
		WithArgs("biz1").
		WillReturnRows(rowsSetting)

	// 2. Mock 表配置（两张表）
	rowsTables := sqlmock.NewRows([]string{"table_name", "is_searchable", "allow_create", "allow_update", "allow_delete"}).
		AddRow("main", true, true, true, true).
		AddRow("sub", false, false, false, false)
	mock.ExpectQuery("SELECT table_name, is_searchable, allow_create, allow_update, allow_delete FROM biz_searchable_tables").
		WithArgs("biz1").
		WillReturnRows(rowsTables)

	// 3. Mock 字段(main表有两个字段)
	rowsFieldsMain := sqlmock.NewRows([]string{"field_name", "is_searchable", "is_returnable", "data_type"}).
		AddRow("id", true, true, "int").
		AddRow("name", false, true, "string")
	mock.ExpectQuery("SELECT field_name, is_searchable, is_returnable, data_type FROM biz_table_field_settings").
		WithArgs("biz1", "main").
		WillReturnRows(rowsFieldsMain)

	// 4. Mock 字段(sub表无字段)
	rowsFieldsSub := sqlmock.NewRows([]string{"field_name", "is_searchable", "is_returnable", "data_type"})
	mock.ExpectQuery("SELECT field_name, is_searchable, is_returnable, data_type FROM biz_table_field_settings").
		WithArgs("biz1", "sub").
		WillReturnRows(rowsFieldsSub)

	cfg, err := svc.loadBizQueryConfigFromDB(ctx, "biz1")
	if err != nil {
		t.Fatalf("主流程返回错误: %v", err)
	}
	if cfg == nil || cfg.BizName != "biz1" || !cfg.IsPubliclySearchable || cfg.DefaultQueryTable != "main" {
		t.Fatalf("业务组配置信息不对: %+v", cfg)
	}
	if len(cfg.Tables) != 2 {
		t.Fatalf("表数量应为2, 实际: %+v", cfg.Tables)
	}
	if len(cfg.Tables["main"].Fields) != 2 || cfg.Tables["sub"].Fields == nil {
		t.Fatalf("字段数量或字段为空: %+v", cfg.Tables)
	}
}

// ===============================
// 查无业务总体配置
// ===============================
func TestLoadBizQueryConfigFromDB_NoRows(t *testing.T) {
	svc, mock, teardown := newTestService(t)
	defer teardown()
	ctx := context.Background()

	mock.ExpectQuery("SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings").
		WithArgs("unknown").
		WillReturnRows(sqlmock.NewRows([]string{"is_publicly_searchable", "default_query_table"}))

	cfg, err := svc.loadBizQueryConfigFromDB(ctx, "unknown")
	if err != nil {
		t.Fatalf("查无数据时应无报错, 实际: %v", err)
	}
	if cfg != nil {
		t.Fatalf("查无业务应返回nil: %+v", cfg)
	}
}

// ===============================
// 总体配置SQL报错
// ===============================
func TestLoadBizQueryConfigFromDB_OverallError(t *testing.T) {
	svc, mock, teardown := newTestService(t)
	defer teardown()
	ctx := context.Background()

	mock.ExpectQuery("SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings").
		WithArgs("errcase").
		WillReturnError(errors.New("fail"))
	cfg, err := svc.loadBizQueryConfigFromDB(ctx, "errcase")
	if err == nil || cfg != nil {
		t.Fatalf("总体配置SQL异常应报错且cfg为nil, 实际: cfg=%+v, err=%v", cfg, err)
	}
}

// ===============================
// 表配置SQL报错
// ===============================
func TestLoadBizQueryConfigFromDB_TableError(t *testing.T) {
	svc, mock, teardown := newTestService(t)
	defer teardown()
	ctx := context.Background()

	rowsSetting := sqlmock.NewRows([]string{"is_publicly_searchable", "default_query_table"}).
		AddRow(false, nil)
	mock.ExpectQuery("SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings").
		WithArgs("tableerr").
		WillReturnRows(rowsSetting)

	mock.ExpectQuery("SELECT table_name, is_searchable, allow_create, allow_update, allow_delete FROM biz_searchable_tables").
		WithArgs("tableerr").
		WillReturnError(errors.New("tablefail"))

	cfg, err := svc.loadBizQueryConfigFromDB(ctx, "tableerr")
	if err == nil || cfg != nil {
		t.Fatalf("表配置SQL异常应报错且cfg为nil, 实际: cfg=%+v, err=%v", cfg, err)
	}
}

// ===============================
// 字段配置SQL报错（不会影响表，只跳过字段）
// ===============================
func TestLoadBizQueryConfigFromDB_FieldError(t *testing.T) {
	svc, mock, teardown := newTestService(t)
	defer teardown()
	ctx := context.Background()

	rowsSetting := sqlmock.NewRows([]string{"is_publicly_searchable", "default_query_table"}).
		AddRow(false, nil)
	mock.ExpectQuery("SELECT is_publicly_searchable, default_query_table FROM biz_overall_settings").
		WithArgs("fielderr").
		WillReturnRows(rowsSetting)

	rowsTables := sqlmock.NewRows([]string{"table_name", "is_searchable", "allow_create", "allow_update", "allow_delete"}).
		AddRow("main", false, false, false, false)
	mock.ExpectQuery("SELECT table_name, is_searchable, allow_create, allow_update, allow_delete FROM biz_searchable_tables").
		WithArgs("fielderr").
		WillReturnRows(rowsTables)

	mock.ExpectQuery("SELECT field_name, is_searchable, is_returnable, data_type FROM biz_table_field_settings").
		WithArgs("fielderr", "main").
		WillReturnError(errors.New("fieldfail"))

	cfg, err := svc.loadBizQueryConfigFromDB(ctx, "fielderr")
	if err != nil {
		t.Fatalf("字段配置SQL异常不应影响主流程, 实际报错: %v", err)
	}
	if len(cfg.Tables) != 1 {
		t.Fatalf("表数量错误: %+v", cfg.Tables)
	}
	if len(cfg.Tables["main"].Fields) != 0 {
		t.Fatalf("字段SQL报错时应无字段，实际: %+v", cfg.Tables["main"].Fields)
	}
}

// ===============================
// 业务名为空异常
// ===============================
func TestLoadBizQueryConfigFromDB_EmptyBizName(t *testing.T) {
	svc, _, teardown := newTestService(t)
	defer teardown()
	ctx := context.Background()

	cfg, err := svc.loadBizQueryConfigFromDB(ctx, "")
	if err == nil || err.Error() != "bizName 不能为空" {
		t.Fatalf("bizName为空应报错, 实际: err=%v", err)
	}
	if cfg != nil {
		t.Fatalf("bizName为空应cfg为nil, 实际: cfg=%+v", cfg)
	}
}
