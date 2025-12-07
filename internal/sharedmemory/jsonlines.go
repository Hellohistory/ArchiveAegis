// Package sharedmemory 提供使用 mmap 的零拷贝 JSONL 数据读写工具
// 文件位置: internal/sharedmemory/jsonlines.go
package sharedmemory

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	mmap "github.com/edsrzf/mmap-go"
)

// WriteJSONLines 将行级 map 数据写入临时文件，并返回 SharedMemoryHandle。
// 数据使用 JSON Lines 格式存储，配合 mmap 读取可避免 gRPC 中的重复拷贝。
func WriteJSONLines(rows []map[string]any) (*datasourcev1.SharedMemoryHandle, error) {
	if len(rows) == 0 {
		return &datasourcev1.SharedMemoryHandle{Format: "jsonl", Rows: 0}, nil
	}

	tmpFile, err := os.CreateTemp("", "aegis-shm-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("创建共享内存文件失败: %w", err)
	}

	writer := bufio.NewWriter(tmpFile)
	for i, row := range rows {
		if err := json.NewEncoder(writer).Encode(row); err != nil {
			_ = tmpFile.Close()
			return nil, fmt.Errorf("编码第 %d 行失败: %w", i, err)
		}
	}
	if err := writer.Flush(); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("刷新共享内存文件失败: %w", err)
	}

	info, err := tmpFile.Stat()
	if err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("读取共享内存文件信息失败: %w", err)
	}

	filePath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("关闭共享内存文件句柄失败: %w", err)
	}

	return &datasourcev1.SharedMemoryHandle{
		FilePath: filePath,
		Offset:   0,
		Length:   info.Size(),
		Format:   "jsonl",
		Rows:     int64(len(rows)),
		Metadata: map[string]string{
			"created_at": strconv.FormatInt(time.Now().UnixNano(), 10),
		},
	}, nil
}

// ReadJSONLines 根据 SharedMemoryHandle 读取 JSONL 数据并反序列化为 map 切片。
func ReadJSONLines(handle *datasourcev1.SharedMemoryHandle) ([]map[string]any, error) {
	if handle == nil {
		return nil, fmt.Errorf("共享内存句柄为空")
	}
	if handle.FilePath == "" || handle.Format != "jsonl" {
		return nil, fmt.Errorf("共享内存句柄格式无效或缺少文件路径")
	}
	file, err := os.Open(handle.FilePath)
	if err != nil {
		return nil, fmt.Errorf("打开共享内存文件失败: %w", err)
	}
	defer file.Close()

	mm, err := mmap.Map(file, mmap.RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("mmap 映射失败: %w", err)
	}
	defer mm.Unmap()

	data := mm
	if handle.Length > 0 && int64(len(mm)) > handle.Length {
		data = mm[:handle.Length]
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1024), 10*1024*1024)

	results := make([]map[string]any, 0, handle.Rows)
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("解析共享内存数据失败: %w", err)
		}
		results = append(results, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描共享内存数据失败: %w", err)
	}
	return results, nil
}
