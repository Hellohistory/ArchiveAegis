// Package router file: internal/transport/http/router/handlers_data.go
package router

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/service"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// registerDataRoutes 注册所有与数据平面相关的 API 端点。
func registerDataRoutes(group *gin.RouterGroup, deps Dependencies) {
	// 保留旧的端点作为便利的别名
	group.POST("/query", queryHandlerV1(deps.Registry))
	group.POST("/mutate", mutateHandlerV1(deps.Registry))

	// 新增的统一执行端点
	group.POST("/execute", executeHandler(deps.Registry))
}

// =============================================================================
//  数据平面处理器 (Data Plane Handlers)
// =============================================================================

// queryHandlerV1 处理旧版的V1数据查询请求。
// 它接收一个特定的 JSON 结构，并将其转换为 DataQueryRequest Protobuf 消息。
func queryHandlerV1(registry map[string]port.Executor) gin.HandlerFunc {
	type RequestBody struct {
		BizName string                 `json:"biz_name" binding:"required"`
		Query   map[string]interface{} `json:"query" binding:"required"`
	}
	return func(c *gin.Context) {
		var reqBody RequestBody
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			_ = c.Error(err)
			return
		}
		queryStruct, err := structpb.NewStruct(reqBody.Query)
		if err != nil {
			_ = c.Error(fmt.Errorf("创建 query struct 失败: %w", err))
			return
		}
		reqPayload := &v1.DataQueryRequest{Query: queryStruct}
		resPayload := &v1.DataQueryResult{}
		// 复用通用的执行和响应函数
		executeAndRespond(c, registry, reqBody.BizName, reqPayload, resPayload)
	}
}

// mutateHandlerV1 处理旧版的V1数据变更请求。
// 它接收一个特定的 JSON 结构，并将其转换为 DataMutateRequest Protobuf 消息。
func mutateHandlerV1(registry map[string]port.Executor) gin.HandlerFunc {
	type RequestBody struct {
		BizName   string                 `json:"biz_name" binding:"required"`
		Operation string                 `json:"operation" binding:"required"`
		Payload   map[string]interface{} `json:"payload" binding:"required"`
	}
	return func(c *gin.Context) {
		var reqBody RequestBody
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			_ = c.Error(err)
			return
		}
		// 记录审计日志
		slog.Info("审计日志: 收到 Mutate 请求", "user_id", service.ClaimFrom(c.Request).ID, "biz_name", reqBody.BizName, "operation", reqBody.Operation)
		payloadStruct, err := structpb.NewStruct(reqBody.Payload)
		if err != nil {
			_ = c.Error(fmt.Errorf("创建 payload struct 失败: %w", err))
			return
		}
		reqPayload := &v1.DataMutateRequest{Operation: reqBody.Operation, Payload: payloadStruct}
		resPayload := &v1.DataMutateResult{}
		// 复用通用的执行和响应函数
		executeAndRespond(c, registry, reqBody.BizName, reqPayload, resPayload)
	}
}

// executeHandler 是统一的执行器端点，可以处理多种命令。
// 它通过 "command" 字段来动态决定请求和响应的 Protobuf 消息类型，更具扩展性。
func executeHandler(registry map[string]port.Executor) gin.HandlerFunc {
	type RequestBody struct {
		BizName string                 `json:"biz_name" binding:"required"`
		Command string                 `json:"command" binding:"required"`
		Payload map[string]interface{} `json:"payload"`
	}

	return func(c *gin.Context) {
		var reqBody RequestBody
		if err := c.ShouldBindJSON(&reqBody); err != nil {
			_ = c.Error(err)
			return
		}

		var reqPayload proto.Message
		var resPayload proto.Message

		// 根据 command 字段选择对应的 Protobuf 消息类型
		switch reqBody.Command {
		case "DataQuery":
			reqPayload = &v1.DataQueryRequest{}
			resPayload = &v1.DataQueryResult{}
		case "DataMutate":
			reqPayload = &v1.DataMutateRequest{}
			resPayload = &v1.DataMutateResult{}
		case "GetSchema":
			reqPayload = &v1.GetSchemaRequest{}
			resPayload = &v1.SchemaResult{}
		case "TriggerBackup":
			reqPayload = &v1.TriggerBackupRequest{}
			resPayload = &v1.TriggerBackupResult{}
		default:
			_ = c.Error(status.Errorf(codes.Unimplemented, "不支持的命令: %s", reqBody.Command))
			return
		}

		// 如果请求中有 payload，则通过 protojson 将其反序列化到对应的 Protobuf 消息中
		if reqBody.Payload != nil {
			jsonBytes, err := json.Marshal(reqBody.Payload)
			if err != nil {
				_ = c.Error(fmt.Errorf("无法序列化载荷以进行映射: %w", err))
				return
			}

			if err := protojson.Unmarshal(jsonBytes, reqPayload); err != nil {
				_ = c.Error(fmt.Errorf("无法将载荷映射到命令 '%s' 的结构: %w", reqBody.Command, err))
				return
			}
		}

		// 复用通用的执行和响应函数
		executeAndRespond(c, registry, reqBody.BizName, reqPayload, resPayload)
	}
}
