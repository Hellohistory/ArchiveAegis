// Package sqlite file: internal/adapter/datasource/sqlite/manager.go
package sqlite

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
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

// 断言 *Manager 实现新的 port.Executor 接口，编译期校验
var _ port.Executor = (*Manager)(nil)

const (
	debounceDuration = 2 * time.Second
)

// Manager 是 SQLite 数据源适配器的核心结构体。
// 它现在实现了 port.Executor 接口，通过统一的 Execute 方法处理所有请求。
type Manager struct {
	mu sync.RWMutex

	root          string
	group         map[string]map[string]*sql.DB
	dbSchemaCache map[*sql.DB]*dbPhysicalSchemaInfo
	schema        map[string]map[string][]string
	eventTimers   map[string]*time.Timer
	eventTimersMu sync.Mutex
	configService port.QueryAdminConfigService
}

// NewManager 创建一个新的 Manager 实例。
func NewManager(cfgService port.QueryAdminConfigService) *Manager {
	if cfgService == nil {
		log.Fatal("[DBManager] 致命错误: QueryAdminConfigService 实例不能为 nil。")
	}
	m := &Manager{
		group:         make(map[string]map[string]*sql.DB),
		dbSchemaCache: make(map[*sql.DB]*dbPhysicalSchemaInfo),
		schema:        make(map[string]map[string][]string),
		eventTimers:   make(map[string]*time.Timer),
		configService: cfgService,
	}
	return m
}

// Execute 是适配“永恒契约”的统一执行入口，负责将请求分发给具体的处理器。
func (m *Manager) Execute(ctx context.Context, req *v1.RequestEnvelope) (*v1.ResponseEnvelope, error) {
	slog.Debug("内置 SQLite 执行器收到 Execute 请求", "request_id", req.RequestId, "biz", req.BizName, "payload_type", req.Payload.TypeUrl)

	var responsePayload proto.Message
	var err error

	// 通过检查 Payload 的类型 URL 来决定执行何种操作
	switch req.Payload.TypeUrl {
	case _typeUrl(&v1.DataQueryRequest{}):
		responsePayload, err = m.handleDataQuery(ctx, req)
	case _typeUrl(&v1.DataMutateRequest{}):
		responsePayload, err = m.handleDataMutate(ctx, req)
	case _typeUrl(&v1.GetSchemaRequest{}):
		responsePayload, err = m.handleGetSchema(ctx, req)
	default:
		err = status.Errorf(codes.Unimplemented, "内置 SQLite 执行器不支持的载荷类型: %s", req.Payload.TypeUrl)
	}

	// 根据处理结果构建统一的 ResponseEnvelope
	if err != nil {
		slog.Error("内置 SQLite 执行器执行失败", "request_id", req.RequestId, "error", err)
		st, _ := status.FromError(err)
		return &v1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status: &v1.Status{
				Code:    int32(st.Code()),
				Message: st.Message(),
			},
		}, nil
	}

	// 将成功的业务结果载荷打包到 Any 中
	packedPayload, packErr := anypb.New(responsePayload)
	if packErr != nil {
		slog.Error("内置 SQLite 执行器打包响应载荷失败", "request_id", req.RequestId, "error", packErr)
		return &v1.ResponseEnvelope{
			RequestId: req.RequestId,
			Status: &v1.Status{
				Code:    int32(codes.Internal),
				Message: fmt.Sprintf("打包响应载荷失败: %v", packErr),
			},
		}, nil
	}

	return &v1.ResponseEnvelope{
		RequestId: req.RequestId,
		Status:    &v1.Status{Code: int32(codes.OK), Message: "Success"},
		Payload:   packedPayload,
	}, nil
}

// Close 安全地关闭由 Manager 管理的所有数据库连接。
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for bizName, libs := range m.group {
		for libName, db := range libs {
			if err := db.Close(); err != nil {
				log.Printf("ERROR: Closing database %s/%s failed: %v", bizName, libName, err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	m.group = make(map[string]map[string]*sql.DB)
	m.dbSchemaCache = make(map[*sql.DB]*dbPhysicalSchemaInfo)
	return firstErr
}

// Type 实现 port.Executor.Type 接口，返回适配器类型。
func (m *Manager) Type() string {
	return "sqlite_builtin"
}

// Summary 返回一个映射，表示每个业务组 (bizName) 下有哪些库文件 (libName)。
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

// _typeUrl 是一个辅助函数，用于获取 Protobuf 消息的类型 URL
func _typeUrl(m proto.Message) string {
	return "type.googleapis.com/" + string(m.ProtoReflect().Descriptor().FullName())
}
