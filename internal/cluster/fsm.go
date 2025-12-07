// Package cluster 提供基于 Raft 的一致性与成员发现能力
// 文件位置: internal/cluster/fsm.go

package cluster

import (
	"encoding/json"
	"io"
	"log/slog"

	"github.com/hashicorp/raft"
)

// CommandHandler 负责将共识后的指令落地到业务层。
type CommandHandler interface {
	HandleCommand(cmd Command) (any, error)
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
	return &noopSnapshot{}, nil
}

func (f *fsm) Restore(r io.ReadCloser) error {
	if r != nil {
		defer r.Close()
	}
	return nil
}

type noopSnapshot struct{}

func (n *noopSnapshot) Persist(_ raft.SnapshotSink) error { return nil }
func (n *noopSnapshot) Release()                          {}
