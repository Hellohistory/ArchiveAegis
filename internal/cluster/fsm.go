// Package cluster 提供基于 Raft 的一致性与成员发现能力
// 文件位置: internal/cluster/fsm.go

package cluster

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"

	"ArchiveAegis/internal/service/plugin_manager"
	"ArchiveAegis/internal/service/workflow"

	"github.com/hashicorp/raft"
)

// CommandHandler 负责将共识后的指令落地到业务层。
type CommandHandler interface {
	HandleCommand(cmd Command) (any, error)
	SnapshotState(ctx context.Context) (*StateSnapshot, error)
	RestoreState(ctx context.Context, snapshot *StateSnapshot) error
}

// fsm 实现 Raft FSM 接口，用于在每个节点上重放指令。
type fsm struct {
	handler CommandHandler
	logger  *slog.Logger
}

func newFSM(handler CommandHandler, logger *slog.Logger) *fsm {
	return &fsm{handler: handler, logger: logger}
}

// Apply 在所有节点上执行指令，确保元数据一致。
func (f *fsm) Apply(logEntry *raft.Log) interface{} {
	var cmd Command
	if err := json.Unmarshal(logEntry.Data, &cmd); err != nil {
		f.logger.Error("无法解析共识指令", "error", err)
		return err
	}
	resp, err := f.handler.HandleCommand(cmd)
	if err != nil {
		f.logger.Error("业务指令执行失败", "error", err, "type", cmd.Type)
		return err
	}
	return resp
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	state, err := f.handler.SnapshotState(context.Background())
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	return &noopSnapshot{data: data}, nil
}

func (f *fsm) Restore(r io.ReadCloser) error {
	if r != nil {
		defer r.Close()
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	var snapshot StateSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	return f.handler.RestoreState(context.Background(), &snapshot)
}

type noopSnapshot struct {
	data []byte
}

func (n *noopSnapshot) Persist(sink raft.SnapshotSink) error {
	if _, err := sink.Write(n.data); err != nil {
		_ = sink.Cancel()
		return err
	}
	if err := sink.Close(); err != nil {
		_ = sink.Cancel()
		return err
	}
	return nil
}
func (n *noopSnapshot) Release() {}

// StateSnapshot 汇总插件与工作流的当前状态，便于 Raft 快照持久化。
type StateSnapshot struct {
	Plugins   *plugin_manager.Snapshot `json:"plugins"`
	Workflows *workflow.Snapshot       `json:"workflows"`
}
