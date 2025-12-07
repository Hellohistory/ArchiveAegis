// Package sharedmemory 提供 mmap 辅助读写的测试用例
// 文件位置: internal/sharedmemory/jsonlines_test.go
package sharedmemory

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"os"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"
)

func TestWriteAndReadJSONLines(t *testing.T) {
	rows := []map[string]any{
		{"id": 1, "name": "alice"},
		{"id": 2, "name": "bob"},
	}

	handle, err := WriteJSONLines(rows)
	if err != nil {
		t.Fatalf("写入 JSONL 失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(handle.GetFilePath()) })

	got, err := ReadJSONLines(handle)
	if err != nil {
		t.Fatalf("读取 JSONL 失败: %v", err)
	}
	if len(got) != len(rows) {
		t.Fatalf("期望 %d 行，得到 %d 行", len(rows), len(got))
	}
	if got[0]["name"] != "alice" || got[1]["id"].(float64) != float64(2) {
		t.Fatalf("反序列化结果不符合预期: %+v", got)
	}
}

func TestReadJSONLinesHandleValidation(t *testing.T) {
	_, err := ReadJSONLines(nil)
	if err == nil {
		t.Fatal("nil 句柄应当返回错误")
	}

	_, err = ReadJSONLines(&datasourcev1.SharedMemoryHandle{FilePath: "", Format: "jsonl"})
	if err == nil {
		t.Fatal("缺少文件路径应当返回错误")
	}
}

func TestExpandResponseIfHandle(t *testing.T) {
	rows := []map[string]any{{"name": "carol"}}
	handle, err := WriteJSONLines(rows)
	if err != nil {
		t.Fatalf("写入共享内存失败: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(handle.GetFilePath()) })

	packedHandle, err := anypb.New(handle)
	if err != nil {
		t.Fatalf("打包句柄失败: %v", err)
	}

	env := &datasourcev1.ResponseEnvelope{Payload: packedHandle}
	expanded, err := ExpandResponseIfHandle(env)
	if err != nil {
		t.Fatalf("展开句柄失败: %v", err)
	}
	var result datasourcev1.DataQueryResult
	if err := expanded.Payload.UnmarshalTo(&result); err != nil {
		t.Fatalf("解包展开结果失败: %v", err)
	}
	if result.GetData().GetFields()["items"].GetListValue().GetValues()[0].GetStructValue().GetFields()["name"].GetStringValue() != "carol" {
		t.Fatalf("展开结果数据不正确: %+v", result)
	}
}
