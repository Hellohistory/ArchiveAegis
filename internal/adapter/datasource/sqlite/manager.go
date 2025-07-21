// file: internal/adapter/datasource/sqlite/manager.go

package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/pkg/go_plugin_sdk"
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// 编译时断言，确保 Manager 同时实现 port.Executor 和 go_plugin_sdk.Plugin 接口
var _ port.Executor = (*Manager)(nil)
var _ go_plugin_sdk.Plugin = (*Manager)(nil)

const (
	// debounceDuration 定义事件去抖动时间间隔
	debounceDuration = 2 * time.Second
)

// dbInstance 封装单个业务数据库连接及其文件路径
type dbInstance struct {
	conn *sql.DB
	path string
}

// Manager 是 SQLite 数据源适配器的核心结构体，
// 同时实现了插件执行器和插件生命周期接口
type Manager struct {
	mu            sync.RWMutex                      // 读写锁，保护以下字段
	pluginSysDB   *sql.DB                           // 插件自身的系统数据库连接
	root          string                            // 根目录路径
	group         map[string]map[string]*dbInstance // 业务组到库实例的映射
	dbSchemaCache map[*sql.DB]*dbPhysicalSchemaInfo // 单库物理模式缓存
	schema        map[string]map[string][]string    // 逻辑模式缓存
	eventTimers   map[string]*time.Timer            // 事件定时器映射
	eventTimersMu sync.Mutex                        // 保护定时器映射的锁
	configService port.QueryAdminConfigService      // 管理配置服务接口
}

// NewManager 创建并返回一个新的 Manager 实例，
// 需要传入管理配置服务和插件自身的系统数据库连接
func NewManager(cfgService port.QueryAdminConfigService, sysDB *sql.DB) *Manager {
	if cfgService == nil {
		log.Fatal("[DBManager] 致命错误: QueryAdminConfigService 实例不能为空")
	}
	if sysDB == nil {
		log.Fatal("[DBManager] 致命错误: 系统数据库连接实例不能为空")
	}
	return &Manager{
		pluginSysDB:   sysDB,
		group:         make(map[string]map[string]*dbInstance),
		dbSchemaCache: make(map[*sql.DB]*dbPhysicalSchemaInfo),
		schema:        make(map[string]map[string][]string),
		eventTimers:   make(map[string]*time.Timer),
		configService: cfgService,
	}
}

// Execute 是统一的执行入口，根据请求载荷类型分发到对应的处理方法
func (m *Manager) Execute(ctx context.Context, req *v1.RequestEnvelope) (*v1.ResponseEnvelope, error) {
	slog.Debug("内置 SQLite 执行器收到 Execute 请求", "request_id", req.RequestId, "biz", req.BizName, "payload_type", req.Payload.TypeUrl)

	var responsePayload proto.Message
	var err error

	// 预先定义一个默认的成功 action，如果后续操作有更具体的 action，可以覆盖它
	action := "success"

	switch req.Payload.TypeUrl {
	case _typeUrl(&v1.DataQueryRequest{}):
		responsePayload, err = m.handleDataQuery(ctx, req)
		action = "query_success" // 查询成功
	case _typeUrl(&v1.DataMutateRequest{}):
		// 对于写操作，返回更具体的 action
		var mutateReq v1.DataMutateRequest
		if unmarshalErr := req.Payload.UnmarshalTo(&mutateReq); unmarshalErr == nil {
			action = mutateReq.Operation // 例如 "create", "update", "delete"
		}
		responsePayload, err = m.handleDataMutate(ctx, req)
	case _typeUrl(&v1.GetSchemaRequest{}):
		responsePayload, err = m.handleGetSchema(ctx, req)
		action = "get_schema_success"
	case _typeUrl(&v1.TriggerBackupRequest{}):
		responsePayload, err = m.handleTriggerBackup(ctx, req)
		action = "backup_success"
	default:
		err = status.Errorf(codes.Unimplemented, "不支持的载荷类型: %s", req.Payload.TypeUrl)
	}

	if err != nil {
		slog.Error("内置 SQLite 执行器执行失败", "request_id", req.RequestId, "error", err)
		st, _ := status.FromError(err)
		// 失败时，action 默认为 "error"
		return &v1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status: &v1.Status{
				Code:    int32(st.Code()),
				Message: st.Message(),
			},
			Action: "error",
		}, nil
	}

	// 成功处理，但没有返回体
	if responsePayload == nil {
		return &v1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status:    &v1.Status{Code: int32(codes.OK), Message: "Success"},
			Action:    action, // <-- 设置 action
		}, nil
	}

	// 成功处理，有返回体
	packedPayload, packErr := anypb.New(responsePayload)
	if packErr != nil {
		slog.Error("打包响应载荷失败", "request_id", req.RequestId, "error", packErr)
		return &v1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status: &v1.Status{
				Code:    int32(codes.Internal),
				Message: fmt.Sprintf("打包响应载荷失败: %v", packErr),
			},
			Action: "error",
		}, nil
	}

	return &v1.ResponseEnvelope{
		RequestId: req.RequestId,
		Status:    &v1.Status{Code: int32(codes.OK), Message: "Success"},
		Payload:   packedPayload,
		Action:    action,
	}, nil
}

// GracefulShutdown 优雅关闭
func (m *Manager) GracefulShutdown(ctx context.Context) error {
	slog.Info("开始执行 SQLite Manager 优雅关闭")
	if err := m.Close(); err != nil {
		slog.Error("关闭业务数据库失败", "error", err)
	} else {
		slog.Info("所有业务数据库连接已关闭")
	}
	if m.pluginSysDB != nil {
		slog.Info("正在关闭插件系统数据库连接")
		if err := m.pluginSysDB.Close(); err != nil {
			slog.Error("关闭插件系统数据库失败", "error", err)
			return err
		}
		slog.Info("插件系统数据库连接已关闭")
	}
	return nil
}

// Close 安全地关闭所有业务数据库连接
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	log.Printf("正在关闭 %d 个业务组的数据库连接", len(m.group))
	for bizName, libs := range m.group {
		for libName, instance := range libs {
			if err := instance.conn.Close(); err != nil {
				log.Printf("ERROR: 关闭数据库 %s/%s 失败: %v", bizName, libName, err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	m.group = make(map[string]map[string]*dbInstance)
	m.dbSchemaCache = make(map[*sql.DB]*dbPhysicalSchemaInfo)
	return firstErr
}

// Type 返回适配器类型标识
func (m *Manager) Type() string {
	return "sqlite_builtin"
}

// Summary 返回当前加载的业务组及其对应的库文件列表
func (m *Manager) Summary() map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	summaryMap := make(map[string][]string, len(m.group))
	for bizName, libsInBiz := range m.group {
		if len(libsInBiz) > 0 {
			libNames := make([]string, 0, len(libsInBiz))
			for libName := range libsInBiz {
				libNames = append(libNames, libName)
			}
			sort.Strings(libNames)
			summaryMap[bizName] = libNames
		}
	}
	return summaryMap
}

// _typeUrl 返回 Protobuf 消息的完整类型 URL
func _typeUrl(m proto.Message) string {
	return "type.googleapis.com/" + string(m.ProtoReflect().Descriptor().FullName())
}
