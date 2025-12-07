// Package router file: internal/transport/http/router/handlers_meta.go
package router

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/sharedmemory"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// registerMetaRoutes 注册所有与元数据发现相关的 API 端点。
func registerMetaRoutes(group *gin.RouterGroup, deps Dependencies) {
	group.GET("/biz", bizHandlerV1(deps.Registry))
	group.GET("/schema/:bizName", schemaHandlerV1(deps.Registry))
	group.GET("/presentations", presentationsHandlerV1(deps.AdminConfigService))
}

// =============================================================================
//  API 执行器辅助函数 (Shared Executor Helper)
// =============================================================================

// executeAndRespond 是一个高阶辅助函数，处理通用的请求打包、执行和响应解包逻辑。
// 它被元数据平面和数据平面的多个处理器复用。
func executeAndRespond(c *gin.Context, registry map[string]port.Executor, bizName string, reqPayload proto.Message, resPayload proto.Message) {
	executor, exists := registry[bizName]
	if !exists {
		_ = c.Error(port.ErrBizNotFound)
		return
	}

	packedPayload, err := anypb.New(reqPayload)
	if err != nil {
		_ = c.Error(fmt.Errorf("打包请求载荷失败: %w", err))
		return
	}

	envelope := &v1.RequestEnvelope{
		RequestId: uuid.New().String(),
		BizName:   bizName,
		Payload:   packedPayload,
	}

	resEnvelope, err := executor.Execute(c.Request.Context(), envelope)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if resEnvelope, err = sharedmemory.ExpandResponseIfHandle(resEnvelope); err != nil {
		_ = c.Error(err)
		return
	}
	if resEnvelope.Status.Code != int32(codes.OK) {
		_ = c.Error(status.Error(codes.Code(resEnvelope.Status.Code), resEnvelope.Status.Message))
		return
	}

	var responseData any
	if resEnvelope.Payload != nil {
		// 尝试解包到提供的 resPayload 结构体中
		if err := resEnvelope.Payload.UnmarshalTo(resPayload); err != nil {
			_ = c.Error(fmt.Errorf("解包响应载荷失败: %w", err))
			return
		}
		// 根据具体类型决定如何转换为JSON
		switch p := resPayload.(type) {
		case *v1.DataQueryResult:
			responseData = p.GetData().AsMap()
		case *v1.DataMutateResult:
			responseData = p.GetData().AsMap()
		default:
			// 对于像 SchemaResult 这样结构良好的消息，直接让Gin序列化
			responseData = p
		}
	} else {
		// 如果 payload 为空，但操作成功，返回一个成功的空对象
		responseData = gin.H{"success": true}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":   responseData,
		"source": executor.Type(),
	})
}

// =============================================================================
//  元数据平面处理器 (Metadata Plane Handlers)
// =============================================================================

// bizHandlerV1 返回所有已注册的业务（biz）名称列表。
func bizHandlerV1(registry map[string]port.Executor) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizNames := make([]string, 0, len(registry))
		for name := range registry {
			bizNames = append(bizNames, name)
		}
		sort.Strings(bizNames)
		c.JSON(http.StatusOK, gin.H{"data": bizNames})
	}
}

// schemaHandlerV1 返回指定业务和表的数据结构（schema）。
// 它使用通用的 executeAndRespond 辅助函数来与后端执行器通信。
func schemaHandlerV1(registry map[string]port.Executor) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Param("bizName")
		reqPayload := &v1.GetSchemaRequest{TableName: c.Query("tableName")}
		resPayload := &v1.SchemaResult{}
		executeAndRespond(c, registry, bizName, reqPayload, resPayload)
	}
}

// presentationsHandlerV1 返回指定业务和表的默认前端视图（View）配置。
func presentationsHandlerV1(configService port.QueryAdminConfigService) gin.HandlerFunc {
	return func(c *gin.Context) {
		bizName := c.Query("biz")
		tableName := c.Query("table")
		if bizName == "" || tableName == "" {
			_ = c.Error(errors.New("缺少 'biz' 或 'table' 参数"))
			return
		}

		viewConfig, err := configService.GetDefaultViewConfig(c.Request.Context(), bizName, tableName)
		if err != nil {
			_ = c.Error(err)
			return
		}
		if viewConfig == nil {
			_ = c.Error(fmt.Errorf("未找到业务 '%s' 表 '%s' 的默认表现层配置", bizName, tableName))
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": viewConfig})
	}
}
