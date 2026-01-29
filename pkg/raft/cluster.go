package raft

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	taupeer "github.com/taubyte/tau/p2p/peer"
)

const (
	// defaultRetainSnapshots is the default number of snapshots to retain
	defaultRetainSnapshots = 3

	// MaxApplyTimeout is the maximum timeout for Apply operations
	MaxApplyTimeout = 60 * time.Second

	// RaftStoragePrefix is the prefix used for all Raft storage paths
	RaftStoragePrefix = "/raft/"
)

type cluster struct {
	node      taupeer.Node
	namespace string

	ctx    context.Context
	cancel context.CancelFunc

	timeoutPreset      TimeoutPreset
	timeoutConfig      TimeoutConfig
	forceBootstrap     bool
	bootstrapTimeout   time.Duration
	bootstrapThreshold float64
	encryptionCipher   cipher.AEAD

	raft          *raft.Raft
	fsm           FSM
	logStore      *datastoreLogStore
	stable        *datastoreStableStore
	snaps         *fileSnapshotStore
	tracker       *peerTracker
	streamService *raftStreamService
	raftClient    internalClient // Client for remote Raft operations (joining voters, forwarding to leader, etc.)

	closed atomic.Bool
	mu     sync.RWMutex
}

// New creates a new Raft cluster with the given namespace
// Nodes with the same namespace discover each other automatically
func New(node taupeer.Node, namespace string, opts ...Option) (Cluster, error) {
	if node == nil {
		return nil, fmt.Errorf("node is required")
	}

	if namespace == "" {
		return nil, fmt.Errorf("%w: namespace cannot be empty", ErrInvalidNamespace)
	}

	c := &cluster{
		node:               node,
		namespace:          namespace,
		timeoutPreset:      PresetRegional,
		timeoutConfig:      presetConfigs[PresetRegional],
		forceBootstrap:     false,
		bootstrapTimeout:   10 * time.Second,
		bootstrapThreshold: 0.8,
	}

	c.ctx, c.cancel = context.WithCancel(node.Context())

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	if err := c.initialize(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *cluster) initialize() error {
	store := c.node.Store()
	storagePrefix := path.Join(RaftStoragePrefix, c.namespace)

	c.logStore = newLogStore(store, path.Join(storagePrefix, "log"))
	c.stable = newStableStore(store, path.Join(storagePrefix, "stable"))

	snapDir := filepath.Join("/tmp", "tau-raft-snapshots", strings.ReplaceAll(c.namespace, "/", "_"))
	var err error
	c.snaps, err = newSnapshotStore(snapDir, defaultRetainSnapshots)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	c.fsm = newKVFSM(store, storagePrefix)

	raftConfig := c.buildRaftConfig()

	transport, err := c.createTransport()
	if err != nil {
		return fmt.Errorf("failed to create transport: %w", err)
	}

	c.raft, err = raft.NewRaft(raftConfig, c.fsm, c.logStore, c.stable, c.snaps, transport)
	if err != nil {
		return fmt.Errorf("failed to create raft: %w", err)
	}

	c.raftClient, err = newInternalClient(c.node, c.namespace, c.encryptionCipher)
	if err != nil {
		return fmt.Errorf("failed to create raft client: %w", err)
	}

	c.tracker = newPeerTracker(c.node.ID())

	go c.tracker.runDiscoveryAndExchange(c.ctx, c)

	c.streamService, err = newStreamService(c)
	if err != nil {
		return fmt.Errorf("failed to create stream service: %w", err)
	}

	if err := c.handleBootstrap(raftConfig, transport); err != nil {
		return fmt.Errorf("failed to bootstrap: %w", err)
	}

	return nil
}

// handleBootstrap implements autonomous bootstrap with peer consensus
func (c *cluster) handleBootstrap(raftConfig *raft.Config, transport raft.Transport) error {
	var (
		successfullyCompleted bool
		noLeader              bool
	)
	defer func() {
		if successfullyCompleted {
			c.tracker.setDiscoveryInterval(30 * time.Second)
		}
	}()

	if c.forceBootstrap {
		err := c.bootstrapSelf(raftConfig, transport)
		if err == nil {
			successfullyCompleted = true
		}
		return err
	}

	ctx, cancel := context.WithTimeout(c.ctx, c.bootstrapTimeout)
	defer cancel()
	ticker := time.NewTicker(BootstrapCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			goto bootstrap
		case <-ticker.C:
			peers := c.raftProtocolPeers()
			if len(peers) > 0 {
				if successfullyCompleted, _ = c.tryJoinExistingCluster(peers, 1*time.Second); successfullyCompleted {
					return nil
				}
			}
		}
	}

bootstrap:
	threshold := time.Duration(float64(c.bootstrapTimeout) * c.bootstrapThreshold)

	if c.tracker.isLateJoiner(threshold) {
		c.requestVoterJoin(VoterJoinTimeout)
		return nil
	}

	founders := c.tracker.getFoundingMembers(threshold)
	allPeers := c.tracker.allPeers()

	if len(founders) > 1 {
		if successfullyCompleted, noLeader = c.tryJoinExistingCluster(founders, 5*time.Second); successfullyCompleted {
			return nil
		}
		if !noLeader {
			c.requestVoterJoin(VoterJoinTimeout)
			return nil
		}
		if err := c.bootstrapWithPeers(transport, founders); err != nil {
			if err == raft.ErrCantBootstrap {
				c.requestVoterJoin(VoterJoinTimeout)
				return nil
			}
			return err
		}
		successfullyCompleted = true
		return nil
	}

	if len(allPeers) > 0 {
		peers := c.raftProtocolPeers()
		if len(peers) > 0 {
			successfullyCompleted, noLeader = c.tryJoinExistingCluster(peers, 5*time.Second)

			if successfullyCompleted {
				return nil
			}
			if !noLeader {
				c.requestVoterJoin(VoterJoinTimeout)
				return nil
			}
		}
		c.requestVoterJoin(LateJoinerTimeout)
		return nil
	}

	if err := c.bootstrapSelf(raftConfig, transport); err != nil {
		if err == raft.ErrCantBootstrap {
			c.requestVoterJoin(VoterJoinTimeout)
			return nil
		}
		return err
	}
	successfullyCompleted = true
	return nil
}

// bootstrapWithPeers bootstraps a cluster with the agreed-upon peer list
func (c *cluster) bootstrapWithPeers(transport raft.Transport, peers []peer.ID) error {
	servers := make([]raft.Server, 0, len(peers))

	for _, p := range peers {
		var addr raft.ServerAddress
		if p == c.node.ID() {
			addr = transport.LocalAddr()
		} else {
			addr = raft.ServerAddress(p.String())
		}
		servers = append(servers, raft.Server{
			Suffrage: raft.Voter,
			ID:       raft.ServerID(p.String()),
			Address:  addr,
		})
	}

	f := c.raft.BootstrapCluster(raft.Configuration{Servers: servers})
	if err := f.Error(); err != nil {
		return err
	}
	return nil
}

func (c *cluster) bootstrapSelf(raftConfig *raft.Config, transport raft.Transport) error {
	f := c.raft.BootstrapCluster(raft.Configuration{
		Servers: []raft.Server{
			{
				ID:       raftConfig.LocalID,
				Address:  transport.LocalAddr(),
				Suffrage: raft.Voter,
			},
		},
	})
	if err := f.Error(); err != nil {
		return err
	}
	return nil
}

func (c *cluster) buildRaftConfig() *raft.Config {
	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(c.node.ID().String())
	cfg.PreVoteDisabled = false

	timeouts := c.getTimeoutConfig()
	cfg.HeartbeatTimeout = timeouts.HeartbeatTimeout
	cfg.ElectionTimeout = timeouts.ElectionTimeout
	cfg.CommitTimeout = timeouts.CommitTimeout
	cfg.LeaderLeaseTimeout = timeouts.LeaderLeaseTimeout
	cfg.SnapshotInterval = timeouts.SnapshotInterval
	cfg.SnapshotThreshold = timeouts.SnapshotThreshold

	return cfg
}

func (c *cluster) getTimeoutConfig() TimeoutConfig {
	if c.timeoutConfig.HeartbeatTimeout > 0 {
		return c.timeoutConfig
	}
	if cfg, ok := presetConfigs[c.timeoutPreset]; ok {
		return cfg
	}
	return presetConfigs[PresetRegional]
}

func (c *cluster) createTransport() (raft.Transport, error) {
	timeout := c.getTimeoutConfig().HeartbeatTimeout
	transport, err := newNamespaceTransport(c.node.Peer(), c.namespace, timeout, c.encryptionCipher)
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace transport: %w", err)
	}
	return transport, nil
}

func (c *cluster) requestVoterJoin(timeout time.Duration) {
	if c.raftClient == nil || c.tracker == nil {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer cancel()

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var targets []peer.ID
				if leader, err := c.Leader(); err == nil && leader != c.node.ID() {
					targets = []peer.ID{leader}
				} else {
					targets = c.voterJoinTargets()
				}
				if len(targets) > 0 {
					c.raftClient.JoinVoter(c.node.ID(), timeout, targets...)
				}
			}
		}
	}()
}

func (c *cluster) tryJoinExistingCluster(peers []peer.ID, timeout time.Duration) (bool, bool) {
	if c.raftClient == nil {
		return false, false
	}

	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	sawNoLeader := false

	for {
		select {
		case <-ctx.Done():
			return false, sawNoLeader
		case <-ticker.C:
			var targets []peer.ID
			if leader, err := c.Leader(); err == nil && leader != c.node.ID() {
				targets = []peer.ID{leader}
			} else {
				targets = make([]peer.ID, 0, len(peers))
				for _, pid := range peers {
					if pid != c.node.ID() {
						targets = append(targets, pid)
					}
				}
			}
			if len(targets) == 0 {
				continue
			}
			if err := c.raftClient.JoinVoter(c.node.ID(), timeout, targets...); err == nil {
				return true, false
			} else if errors.Is(err, ErrNoLeader) || strings.Contains(err.Error(), ErrNoLeader.Error()) {
				sawNoLeader = true
			}
		}
	}
}

func (c *cluster) protocolPeers(protocolID protocol.ID, includeTracker bool) []peer.ID {
	targets := make(map[peer.ID]struct{})

	if includeTracker && c.tracker != nil {
		for _, pid := range c.tracker.allPeers() {
			if pid == c.node.ID() {
				continue
			}
			targets[pid] = struct{}{}
		}
	}

	for _, pid := range c.node.Peer().Network().Peers() {
		if pid == c.node.ID() {
			continue
		}
		targets[pid] = struct{}{}
	}

	host := c.node.Peer()
	result := make([]peer.ID, 0, len(targets))

	for pid := range targets {
		supported, err := host.Peerstore().SupportsProtocols(pid, protocolID)
		if err != nil {
			continue
		}
		if len(supported) == 0 {
			continue
		}
		result = append(result, pid)
	}
	return result
}

func (c *cluster) voterJoinTargets() []peer.ID {
	return c.protocolPeers(protocol.ID(Protocol(c.namespace)), true)
}

func (c *cluster) raftProtocolPeers() []peer.ID {
	return c.protocolPeers(protocol.ID(TransportProtocol(c.namespace)), false)
}

// Close gracefully shuts down the Raft node
func (c *cluster) Close() error {
	if c.closed.Swap(true) {
		return ErrAlreadyClosed
	}

	if c.cancel != nil {
		c.cancel()
	}

	if c.streamService != nil {
		c.streamService.stop()
	}

	if c.raftClient != nil {
		c.raftClient.Close()
	}

	if c.raft != nil {
		return c.raft.Shutdown().Error()
	}

	return nil
}

func (c *cluster) Namespace() string {
	return c.namespace
}

func (c *cluster) Set(key string, value []byte, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	if !c.IsLeader() {
		return ErrNotLeader
	}

	cmd, err := encodeSetCommand(key, value)
	if err != nil {
		return fmt.Errorf("failed to encode command: %w", err)
	}

	future := c.raft.Apply(cmd, timeout)
	if err := future.Error(); err != nil {
		if err == raft.ErrNotLeader {
			return ErrNotLeader
		}
		return err
	}

	resp := future.Response()
	if fsmResp, ok := resp.(FSMResponse); ok && fsmResp.Error != nil {
		return fsmResp.Error
	}

	return nil
}

func (c *cluster) Get(key string) ([]byte, bool) {
	if c.closed.Load() {
		return nil, false
	}

	if c.fsm == nil {
		return nil, false
	}

	return c.fsm.Get(key)
}

func (c *cluster) Delete(key string, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	if !c.IsLeader() {
		return ErrNotLeader
	}

	cmd, err := encodeDeleteCommand(key)
	if err != nil {
		return fmt.Errorf("failed to encode command: %w", err)
	}

	future := c.raft.Apply(cmd, timeout)
	if err := future.Error(); err != nil {
		if err == raft.ErrNotLeader {
			return ErrNotLeader
		}
		return err
	}

	resp := future.Response()
	if fsmResp, ok := resp.(FSMResponse); ok && fsmResp.Error != nil {
		return fsmResp.Error
	}

	return nil
}

func (c *cluster) Keys(prefix string) []string {
	if c.closed.Load() {
		return []string{}
	}

	if c.fsm == nil {
		return []string{}
	}

	keys := c.fsm.Keys(prefix)
	if keys == nil {
		return []string{}
	}
	return keys
}

func (c *cluster) Apply(cmd []byte, timeout time.Duration) (FSMResponse, error) {
	if c.closed.Load() {
		return FSMResponse{}, ErrShutdown
	}

	if timeout <= 0 {
		return FSMResponse{}, ErrInvalidTimeout
	}
	if timeout > MaxApplyTimeout {
		return FSMResponse{}, ErrInvalidTimeout
	}

	if !c.IsLeader() {
		return FSMResponse{}, ErrNotLeader
	}

	future := c.raft.Apply(cmd, timeout)
	if err := future.Error(); err != nil {
		if err == raft.ErrNotLeader {
			return FSMResponse{}, ErrNotLeader
		}
		return FSMResponse{}, err
	}

	resp := future.Response()
	if fsmResp, ok := resp.(FSMResponse); ok {
		return fsmResp, nil
	}

	return FSMResponse{}, nil
}

func (c *cluster) Barrier(timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	return c.raft.Barrier(timeout).Error()
}

func (c *cluster) IsLeader() bool {
	if c.closed.Load() {
		return false
	}

	return c.raft.State() == raft.Leader
}

func (c *cluster) Leader() (peer.ID, error) {
	if c.closed.Load() {
		return "", ErrShutdown
	}

	addr, _ := c.raft.LeaderWithID()
	if addr == "" {
		return "", ErrNoLeader
	}

	return peer.Decode(string(addr))
}

func (c *cluster) State() raft.RaftState {
	if c.closed.Load() {
		return raft.Shutdown
	}

	return c.raft.State()
}

func (c *cluster) WaitForLeader(ctx context.Context) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			addr, _ := c.raft.LeaderWithID()
			if addr != "" {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-c.ctx.Done():
			return ErrShutdown
		}
	}
}

func (c *cluster) Members() ([]Member, error) {
	if c.closed.Load() {
		return nil, ErrShutdown
	}

	configFuture := c.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return nil, err
	}

	var members []Member
	for _, server := range configFuture.Configuration().Servers {
		peerID, err := peer.Decode(string(server.ID))
		if err != nil {
			continue
		}
		members = append(members, Member{
			ID:       peerID,
			Address:  server.Address,
			Suffrage: server.Suffrage,
		})
	}

	return members, nil
}

func (c *cluster) AddVoter(id peer.ID, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	if !c.IsLeader() {
		return ErrNotLeader
	}

	serverID := raft.ServerID(id.String())
	serverAddr := raft.ServerAddress(id.String())

	configFuture := c.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	for _, server := range configFuture.Configuration().Servers {
		if server.ID == serverID {
			return nil
		}
	}

	future := c.raft.AddVoter(serverID, serverAddr, 0, timeout)
	if err := future.Error(); err != nil {
		return err
	}
	return nil
}

func (c *cluster) RemoveServer(id peer.ID, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	if !c.IsLeader() {
		return ErrNotLeader
	}

	serverID := raft.ServerID(id.String())
	future := c.raft.RemoveServer(serverID, 0, timeout)
	return future.Error()
}

func (c *cluster) TransferLeadership() error {
	if c.closed.Load() {
		return ErrShutdown
	}

	return c.raft.LeadershipTransfer().Error()
}
