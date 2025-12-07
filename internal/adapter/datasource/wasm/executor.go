// Package wasm 提供基于 wazero 的 WebAssembly 插件执行器实现。
// 文件位置: internal/adapter/datasource/wasm/executor.go
package wasm

import (
	datasourcev1 "ArchiveAegis/gen/go/proto/datasource/v1"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/core/port"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	defaultRuntimeName       = "wasm_plugin"
	defaultExportAlloc       = "alloc"
	defaultExportExecute     = "execute"
	defaultExportHealthCheck = "health_check"
	defaultExportPluginInfo  = "plugin_info"
	hostModuleName           = "env"
	hostLogFunctionName      = "host_log"
)

// Executor 基于 wazero 在宿主进程内运行 wasm 插件，使用导出函数完成请求编解码。
type Executor struct {
	runtime      wazero.Runtime
	module       api.Module
	alloc        api.Function
	execFn       api.Function
	healthFn     api.Function
	infoFn       api.Function
	pluginInfo   *datasourcev1.GetPluginInfoResponse
	runtimeName  string
	jsonOptions  protojson.MarshalOptions
	unmarshalOpt protojson.UnmarshalOptions
}

var _ port.Executor = (*Executor)(nil)

// NewExecutor 从 wasm 文件创建执行器，并调用插件自声明的 plugin_info 以完成握手校验。
func NewExecutor(ctx context.Context, wasmPath string, execCfg domain.Execution) (*Executor, *datasourcev1.GetPluginInfoResponse, error) {
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, nil, fmt.Errorf("读取 wasm 文件失败: %w", err)
	}

	cfg := normalizeWasmConfig(execCfg.Wasm)
	rt := wazero.NewRuntime(ctx)

	if cfg.EnableWASI {
		if _, err = wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
			_ = rt.Close(ctx)
			return nil, nil, fmt.Errorf("实例化 WASI 失败: %w", err)
		}
	}

	if err = registerHostLogging(ctx, rt); err != nil {
		_ = rt.Close(ctx)
		return nil, nil, err
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, nil, fmt.Errorf("编译 wasm 模块失败: %w", err)
	}

	module, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		_ = rt.Close(ctx)
		return nil, nil, fmt.Errorf("实例化 wasm 模块失败: %w", err)
	}

	allocFn := module.ExportedFunction(cfg.ExportedAlloc)
	if allocFn == nil {
		_ = rt.Close(ctx)
		return nil, nil, fmt.Errorf("未找到导出函数 %s", cfg.ExportedAlloc)
	}

	execFn := module.ExportedFunction(cfg.ExportedExecute)
	healthFn := module.ExportedFunction(cfg.ExportedHealthCheck)
	infoFn := module.ExportedFunction(cfg.ExportedPluginInfo)
	if execFn == nil || healthFn == nil || infoFn == nil {
		_ = module.Close(ctx)
		_ = rt.Close(ctx)
		return nil, nil, errors.New("wasm 插件必须导出 execute/health_check/plugin_info 函数")
	}

	w := &Executor{
		runtime:     rt,
		module:      module,
		alloc:       allocFn,
		execFn:      execFn,
		healthFn:    healthFn,
		infoFn:      infoFn,
		runtimeName: defaultRuntimeName,
		jsonOptions: protojson.MarshalOptions{EmitUnpopulated: false, UseProtoNames: true},
		unmarshalOpt: protojson.UnmarshalOptions{
			DiscardUnknown: true,
		},
	}

	infoPayload, err := w.callFunction(ctx, w.infoFn, []byte{})
	if err != nil {
		_ = w.Close()
		return nil, nil, fmt.Errorf("调用 wasm plugin_info 失败: %w", err)
	}

	var pluginInfo datasourcev1.GetPluginInfoResponse
	if err = w.unmarshalOpt.Unmarshal(infoPayload, &pluginInfo); err != nil {
		_ = w.Close()
		return nil, nil, fmt.Errorf("解析 plugin_info 响应失败: %w", err)
	}
	if pluginInfo.ContractVersion == nil {
		_ = w.Close()
		return nil, nil, errors.New("plugin_info 缺少协议版本声明")
	}

	w.pluginInfo = &pluginInfo
	return w, &pluginInfo, nil
}

// Execute 将 RequestEnvelope 编码为 JSON 字节传入 wasm 函数，并解析返回结果。
func (w *Executor) Execute(ctx context.Context, req *datasourcev1.RequestEnvelope) (*datasourcev1.ResponseEnvelope, error) {
	payload, err := w.jsonOptions.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	raw, err := w.callFunction(ctx, w.execFn, payload)
	if err != nil {
		return nil, err
	}

	var resp datasourcev1.ResponseEnvelope
	if err = w.unmarshalOpt.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("解析执行结果失败: %w", err)
	}
	return &resp, nil
}

// HealthCheck 调用 wasm 导出的健康检查函数，若返回错误将其包装。
func (w *Executor) HealthCheck(ctx context.Context) error {
	if _, err := w.callFunction(ctx, w.healthFn, []byte{}); err != nil {
		return fmt.Errorf("wasm 健康检查失败: %w", err)
	}
	return nil
}

// Type 返回执行器的运行时类型标识。
func (w *Executor) Type() string {
	return w.runtimeName
}

// Close 释放 wasm 模块与运行时资源。
func (w *Executor) Close() error {
	ctx := context.Background()
	if w.module != nil {
		_ = w.module.Close(ctx)
	}
	if w.runtime != nil {
		return w.runtime.Close(ctx)
	}
	return nil
}

// PluginInfo 返回创建时握手得到的插件元信息。
func (w *Executor) PluginInfo() *datasourcev1.GetPluginInfoResponse {
	return w.pluginInfo
}

func (w *Executor) callFunction(ctx context.Context, fn api.Function, payload []byte) ([]byte, error) {
	ptr, err := w.allocateGuestBuffer(ctx, payload)
	if err != nil {
		return nil, err
	}

	results, err := fn.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		return nil, fmt.Errorf("调用 wasm 函数失败: %w", err)
	}
	if len(results) < 2 {
		return nil, errors.New("wasm 函数返回结果数量不足，期望 [ptr, len]")
	}

	offset, length := uint32(results[0]), uint32(results[1])
	mem := w.module.Memory()
	if mem == nil {
		return nil, errors.New("wasm 模块未导出内存")
	}
	data, ok := mem.Read(offset, length)
	if !ok {
		return nil, fmt.Errorf("无法读取 wasm 返回数据，offset=%d length=%d", offset, length)
	}
	return append([]byte{}, data...), nil
}

func (w *Executor) allocateGuestBuffer(ctx context.Context, data []byte) (uint32, error) {
	results, err := w.alloc.Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("调用 wasm alloc 失败: %w", err)
	}
	if len(results) == 0 {
		return 0, errors.New("wasm alloc 未返回指针")
	}
	ptr := uint32(results[0])
	if !w.module.Memory().Write(ptr, data) {
		return 0, fmt.Errorf("写入 wasm 内存失败，offset=%d size=%d", ptr, len(data))
	}
	return ptr, nil
}

func registerHostLogging(ctx context.Context, rt wazero.Runtime) error {
	logger := slog.Default()
	_, err := rt.NewHostModuleBuilder(hostModuleName).
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, module api.Module, stack []uint64) { //nolint:revive
			mem := module.Memory()
			if mem == nil || len(stack) < 2 {
				return
			}
			ptr := uint32(stack[0])
			size := uint32(stack[1])
			if data, ok := mem.Read(ptr, size); ok {
				logger.Info(string(data))
			}
		}), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}, nil).
		Export(hostLogFunctionName).
		Instantiate(ctx)
	if err != nil {
		return fmt.Errorf("注册 host 日志函数失败: %w", err)
	}
	return nil
}

func normalizeWasmConfig(cfg *domain.WasmConfig) *domain.WasmConfig {
	if cfg == nil {
		cfg = &domain.WasmConfig{}
	}
	if cfg.ExportedAlloc == "" {
		cfg.ExportedAlloc = defaultExportAlloc
	}
	if cfg.ExportedExecute == "" {
		cfg.ExportedExecute = defaultExportExecute
	}
	if cfg.ExportedHealthCheck == "" {
		cfg.ExportedHealthCheck = defaultExportHealthCheck
	}
	if cfg.ExportedPluginInfo == "" {
		cfg.ExportedPluginInfo = defaultExportPluginInfo
	}
	return cfg
}
