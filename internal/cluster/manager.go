// Package cluster 提供基于 Raft 的一致性与成员发现能力
// 文件位置: internal/cluster/manager.go

package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
)

// Config 描述集群与共识层相关的配置。
type Config struct {
	NodeID        string   `mapstructure:"node_id"`
	RaftDir       string   `mapstructure:"raft_dir"`
	RaftBind      string   `mapstructure:"raft_bind"`
	RaftAdvertise string   `mapstructure:"raft_advertise"`
	GossipBind    string   `mapstructure:"gossip_bind"`
	Join          []string `mapstructure:"join"`
	Bootstrap     bool     `mapstructure:"bootstrap"`
}

// NotLeaderError 在非领导节点接收写入请求时返回。
type NotLeaderError struct {
	LeaderAddress string
}

func (n *NotLeaderError) Error() string {
	if n.LeaderAddress == "" {
		return "当前节点不是 Leader，且未知 Leader 地址"
	}
	return fmt.Sprintf("当前节点不是 Leader，请请求 %s", n.LeaderAddress)
}

// Manager 封装 Raft 与 Gossip 实例，提供共识写入能力。
type Manager struct {
	raftNode   *raft.Raft
	memberlist *memberlist.Memberlist
	logger     *slog.Logger
	config     Config
}

// NewManager 启动集群子系统，包括 memberlist 与 Raft。
func NewManager(cfg Config, handler CommandHandler, logger *slog.Logger) (*Manager, error) {
	if cfg.NodeID == "" {
		return nil, errors.New("cluster.node_id 不能为空")
	}
	if logger == nil {
		logger = slog.Default()
	}

	if cfg.RaftDir == "" {
		cfg.RaftDir = filepath.Join(os.TempDir(), "aegis-raft-"+cfg.NodeID)
	}
	if cfg.RaftBind == "" {
		cfg.RaftBind = "127.0.0.1:0"
	}
	if cfg.GossipBind == "" {
		cfg.GossipBind = "0.0.0.0:0"
	}

	if err := os.MkdirAll(cfg.RaftDir, 0755); err != nil {
		return nil, fmt.Errorf("创建 Raft 目录失败: %w", err)
	}

	m := &Manager{logger: logger, config: cfg}
	if err := m.initMemberlist(); err != nil {
		return nil, err
	}
	if err := m.initRaft(handler); err != nil {
		return nil, err
	}
	return m, nil
}

// IsLeader 返回当前节点是否为 Leader。
func (m *Manager) IsLeader() bool {
	return m.raftNode.State() == raft.Leader
}

// LeaderAddress 返回当前 Raft 视图中的 Leader 通信地址。
func (m *Manager) LeaderAddress() string {
	return string(m.raftNode.Leader())
}

// ApplyCommand 在 Leader 上提交指令，并等待复制结果。
func (m *Manager) ApplyCommand(ctx context.Context, cmd Command) (any, error) {
	if !m.IsLeader() {
		return nil, &NotLeaderError{LeaderAddress: m.LeaderAddress()}
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("序列化指令失败: %w", err)
	}
	applyTimeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		applyTimeout = time.Until(deadline)
	}
	future := m.raftNode.Apply(data, applyTimeout)
	if err := future.Error(); err != nil {
		return nil, err
	}
	return future.Response(), nil
}

func (m *Manager) initMemberlist() error {
	cfg := memberlist.DefaultLANConfig()
	cfg.Name = m.config.NodeID
	if host, port, err := net.SplitHostPort(m.config.GossipBind); err == nil {
		cfg.BindAddr = host
		cfg.BindPort = parsePort(port)
	}
	list, err := memberlist.Create(cfg)
	if err != nil {
		return fmt.Errorf("创建 memberlist 失败: %w", err)
	}
	if len(m.config.Join) > 0 {
		if _, err := list.Join(m.config.Join); err != nil {
			m.logger.Warn("加入 Gossip 集群失败", "error", err)
		}
	}
	m.memberlist = list
	return nil
}

func (m *Manager) initRaft(handler CommandHandler) error {
	rc := raft.DefaultConfig()
	rc.LocalID = raft.ServerID(m.config.NodeID)
	rc.SnapshotInterval = 10 * time.Minute
	rc.SnapshotThreshold = 1024

	fsm := newFSM(handler, m.logger)

	boltStore, err := raftboltdb.NewBoltStore(filepath.Join(m.config.RaftDir, "raft.db"))
	if err != nil {
		return fmt.Errorf("创建 Bolt 日志存储失败: %w", err)
	}
	stableStore := boltStore
	logStore := boltStore

	snapshotStore, err := raft.NewFileSnapshotStore(m.config.RaftDir, 3, os.Stdout)
	if err != nil {
		return fmt.Errorf("创建快照存储失败: %w", err)
	}

	var advertise net.Addr
	if m.config.RaftAdvertise != "" {
		if addr, err := net.ResolveTCPAddr("tcp", m.config.RaftAdvertise); err == nil {
			advertise = addr
		}
	}
	transport, err := raft.NewTCPTransport(m.config.RaftBind, advertise, 3, 10*time.Second, os.Stdout)
	if err != nil {
		return fmt.Errorf("创建 Raft 传输层失败: %w", err)
	}

	node, err := raft.NewRaft(rc, fsm, logStore, stableStore, snapshotStore, transport)
	if err != nil {
		return fmt.Errorf("初始化 Raft 节点失败: %w", err)
	}

	if m.config.Bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{ID: rc.LocalID, Address: transport.LocalAddr()},
			},
		}
		_ = node.BootstrapCluster(configuration)
	}
	m.raftNode = node
	return nil
}

func parsePort(port string) int {
	p, err := net.LookupPort("tcp", port)
	if err != nil {
		if value, convErr := fmt.Sscanf(port, "%d", &p); convErr == nil && value == 1 {
			return p
		}
		return 0
	}
	return p
}
