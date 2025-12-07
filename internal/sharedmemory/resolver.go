// Package sharedmemory 提供共享内存句柄展开工具
// 文件位置: internal/sharedmemory/resolver.go
package sharedmemory

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"fmt"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

// ExpandResponseIfHandle 检测响应中是否包含 SharedMemoryHandle，若是则读取 mmap 数据并回填为 DataQueryResult。
func ExpandResponseIfHandle(env *datasourcev1.ResponseEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	if env == nil || env.Payload == nil {
		return env, nil
	}

	var handle datasourcev1.SharedMemoryHandle
	if !env.Payload.MessageIs(&handle) {
		return env, nil
	}
	if err := env.Payload.UnmarshalTo(&handle); err != nil {
		return nil, fmt.Errorf("解包共享内存句柄失败: %w", err)
	}

	rows, err := ReadJSONLines(&handle)
	if err != nil {
		return nil, err
	}

	items := make([]any, len(rows))
	for i, row := range rows {
		items[i] = row
	}

	resultStruct, err := structpb.NewStruct(map[string]any{
		"items": items,
		"total": handle.GetRows(),
	})
	if err != nil {
		return nil, fmt.Errorf("构建共享内存查询结果失败: %w", err)
	}

	packed, err := anypb.New(&datasourcev1.DataQueryResult{Data: resultStruct})
	if err != nil {
		return nil, fmt.Errorf("打包共享内存查询结果失败: %w", err)
	}

	env.Payload = packed
	return env, nil
}
