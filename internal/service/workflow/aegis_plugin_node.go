// file: internal/service/workflow/aegis_plugin_node.go

// Package workflow 包含了与工作流执行相关的逻辑。
// 这个文件专门负责处理 'PLUGIN' 类型的节点，将其转换为可执行的 AegisNode。
package workflow

import (
	v1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/port"
	"ArchiveAegis/internal/sharedmemory"
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/jmespath/go-jmespath"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// pluginNodeConfig 定义了 PLUGIN 类型节点的详细配置结构。
type pluginNodeConfig struct {
	BizName        string            `json:"biz_name"`
	Command        string            `json:"command"`
	InputMappings  map[string]string `json:"input_mappings"`
	PostResultPath string            `json:"post_result_path"`
}

// aegisPluginNode 是一个为插件调用专门定制的 AegisNode 实现。
type aegisPluginNode struct {
	aegisNodeCore
	config   pluginNodeConfig
	executor port.Executor
}

// Execute 实现了 AegisNode 接口，定义了插件节点的完整执行逻辑。
func (apn *aegisPluginNode) Execute(ctx context.Context, dataCtx *AegisDataContext) (string, any, error) {
	reqPayload, err := apn.prepareRequest(dataCtx)
	if err != nil {
		return "error", nil, fmt.Errorf("节点 '%s' 准备请求失败: %w", apn.id, err)
	}

	resEnvelope, err := apn.executePlugin(ctx, reqPayload)
	if err != nil {
		return "error", nil, fmt.Errorf("节点 '%s' 执行插件失败: %w", apn.id, err)
	}

	return apn.processResponse(resEnvelope)
}

// prepareRequest 从工作流上下文中提取数据，并组装成插件需要的 Protobuf 请求消息。
func (apn *aegisPluginNode) prepareRequest(dataCtx *AegisDataContext) (proto.Message, error) {
	inputSource := map[string]any{
		"initial_params": dataCtx.InitialParams,
		"node_outputs":   dataCtx.NodeOutputs,
	}
	mappedParams := make(map[string]any)
	for key, jmespathQuery := range apn.config.InputMappings {
		result, err := jmespath.Search(jmespathQuery, inputSource)
		if err != nil {
			return nil, fmt.Errorf("为字段 '%s' 执行JMESPath查询 '%s' 失败: %w", key, jmespathQuery, err)
		}
		mappedParams[key] = result
	}
	reqPayload, err := getProtoMessageForCommand(apn.config.Command)
	if err != nil {
		return nil, err
	}
	jsonBytes, err := json.Marshal(mappedParams)
	if err != nil {
		return nil, fmt.Errorf("序列化映射参数为JSON失败: %w", err)
	}
	if err := protojson.Unmarshal(jsonBytes, reqPayload); err != nil {
		return nil, fmt.Errorf("无法将参数映射到命令 '%s' 的结构: %w", apn.config.Command, err)
	}
	return reqPayload, nil
}

// executePlugin 调用插件执行器。
func (apn *aegisPluginNode) executePlugin(ctx context.Context, reqPayload proto.Message) (*v1.ResponseEnvelope, error) {
	packedPayload, err := anypb.New(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("打包 Protobuf Any 失败: %w", err)
	}
	envelope := &v1.RequestEnvelope{
		BizName: apn.config.BizName,
		Payload: packedPayload,
	}
	log.Printf("信息: [PluginNode] 正在调用插件 '%s' 的 '%s' 命令...", apn.config.BizName, apn.config.Command)
	resEnvelope, err := apn.executor.Execute(ctx, envelope)
	if err != nil {
		return nil, err
	}
	if resEnvelope, err = sharedmemory.ExpandResponseIfHandle(resEnvelope); err != nil {
		return nil, err
	}
	return resEnvelope, nil
}

// processResponse 解析插件返回的 ResponseEnvelope，提取所需数据作为此节点的最终输出。
func (apn *aegisPluginNode) processResponse(resEnvelope *v1.ResponseEnvelope) (string, any, error) {
	if resEnvelope.Status != nil && resEnvelope.Status.Code != 0 {
		return "error", nil, fmt.Errorf("插件 '%s' 返回业务错误: code=%d, message=%s",
			apn.config.BizName, resEnvelope.Status.Code, resEnvelope.Status.Message)
	}

	unpackedPayload, err := resEnvelope.Payload.UnmarshalNew()
	if err != nil {
		return "error", nil, fmt.Errorf("解包插件响应失败: %w", err)
	}

	jsonBytes, err := protojson.Marshal(unpackedPayload)
	if err != nil {
		return "error", nil, fmt.Errorf("序列化插件响应为JSON失败: %w", err)
	}

	var payloadMap map[string]any
	if err := json.Unmarshal(jsonBytes, &payloadMap); err != nil {
		return "error", nil, fmt.Errorf("反序列化插件响应JSON为map失败: %w", err)
	}

	var finalResult any = payloadMap
	if apn.config.PostResultPath != "" {
		extractedResult, err := jmespath.Search(apn.config.PostResultPath, payloadMap)
		if err != nil {
			return "error", nil, fmt.Errorf("从插件响应中提取路径 '%s' 失败: %w", apn.config.PostResultPath, err)
		}
		finalResult = extractedResult
	}

	// 如果插件没有提供 action，这里会返回空字符串 ""，
	// 在工作流引擎中，空字符串的 action 会被当作 "default" 处理，行为依然可控。
	return resEnvelope.Action, finalResult, nil
}

// createAegisPluginNode 是创建插件节点的工厂函数。
func (s *Service) createAegisPluginNode(nodeID string, configData json.RawMessage) (AegisNode, error) {
	var config pluginNodeConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("解析插件节点 '%s' 配置失败: %w", nodeID, err)
	}
	executor, ok := s.executorProvider.GetExecutor(config.BizName)
	if !ok {
		return nil, fmt.Errorf("找不到 biz_name 为 '%s' 的插件执行器", config.BizName)
	}
	node := &aegisPluginNode{
		aegisNodeCore: aegisNodeCore{id: nodeID},
		config:        config,
		executor:      executor,
	}
	return node, nil
}

// getProtoMessageForCommand 是一个辅助函数，根据命令返回空的 proto.Message 实例。
func getProtoMessageForCommand(command string) (proto.Message, error) {
	switch command {
	case "DataQuery":
		return &v1.DataQueryRequest{}, nil
	case "DataMutate":
		return &v1.DataMutateRequest{}, nil
	default:
		return nil, fmt.Errorf("不支持的插件命令: %s", command)
	}
}
