package raft

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	// defaultRetainSnapshots is the default number of snapshots to retain
	defaultRetainSnapshots = 3
)

// cluster implements the Cluster interface
type cluster struct {
	node      Node
	namespace string
	cfg       *config

	raft     *raft.Raft
	fsm      *kvFSM
	logStore *datastoreLogStore
	stable   *datastoreStableStore
	snaps    *fileSnapshotStore
	tracker  *peerTracker

	// Stream service for handling p2p commands
	streamService *raftStreamService

	// Client for forwarding to leader
	raftClient *Client

	leaderCh chan bool
	closed   atomic.Bool
	mu       sync.RWMutex
}

// New creates a new Raft cluster with the given namespace
// Nodes with the same namespace discover each other automatically
func New(node Node, namespace string, opts ...Option) (Cluster, error) {
	if node == nil {
		return nil, fmt.Errorf("node is required")
	}

	if !strings.HasPrefix(namespace, "/raft/") {
		return nil, fmt.Errorf("%w: namespace must start with /raft/", ErrInvalidNamespace)
	}

	cfg := defaultConfig(namespace)
	for _, opt := range opts {
		opt(cfg)
	}

	c := &cluster{
		node:      node,
		namespace: namespace,
		cfg:       cfg,
		leaderCh:  make(chan bool, 1),
	}

	if err := c.initialize(); err != nil {
		return nil, err
	}

	return c, nil
}

// initialize sets up all the Raft components
func (c *cluster) initialize() error {
	store := c.node.Store()
	prefix := c.namespace + "/"

	// Create storage backends
	c.logStore = NewLogStore(store, prefix+"log/")
	c.stable = NewStableStore(store, prefix+"stable/")

	// Create snapshot store (file-based for simplicity)
	// Use a subdirectory under the node's context
	snapDir := filepath.Join("/tmp", "tau-raft-snapshots", strings.ReplaceAll(c.namespace, "/", "_"))
	var err error
	c.snaps, err = NewSnapshotStore(snapDir, defaultRetainSnapshots)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	// Create FSM
	if c.cfg.customFSM != nil {
		// Wrap custom FSM
		c.fsm = nil // Custom FSM path - not using built-in kvFSM
	} else {
		c.fsm = NewKVFSM(store, prefix)
	}

	// Create Raft configuration
	raftConfig := c.buildRaftConfig()

	// Create transport
	transport, err := c.createTransport()
	if err != nil {
		return fmt.Errorf("failed to create transport: %w", err)
	}

	// Create the Raft instance
	var fsmToUse raft.FSM
	if c.cfg.customFSM != nil {
		fsmToUse = &fsmAdapter{fsm: c.cfg.customFSM}
	} else {
		fsmToUse = &fsmAdapter{fsm: c.fsm}
	}

	c.raft, err = raft.NewRaft(raftConfig, fsmToUse, c.logStore, c.stable, c.snaps, transport)
	if err != nil {
		return fmt.Errorf("failed to create raft: %w", err)
	}

	// Create raft client for forwarding to leader
	c.raftClient, err = NewClient(c.node, c.namespace)
	if err != nil {
		return fmt.Errorf("failed to create raft client: %w", err)
	}

	// Create tracker before stream service since handlers use it
	c.tracker = newPeerTracker(c.node.ID())

	// Create stream service for handling p2p commands
	c.streamService, err = newStreamService(c)
	if err != nil {
		return fmt.Errorf("failed to create stream service: %w", err)
	}

	// Handle bootstrap - autonomous discovery-first approach
	if err := c.handleBootstrap(raftConfig, transport); err != nil {
		return fmt.Errorf("failed to bootstrap: %w", err)
	}

	// Start leader monitoring
	go c.monitorLeadership()

	// Start peer discovery (for ongoing membership changes)
	go c.discoverPeers()

	return nil
}

// handleBootstrap implements autonomous bootstrap with time-based threshold:
// 1. If forceBootstrap is set, bootstrap immediately as single-node
// 2. Otherwise, discover peers and exchange lists for bootstrapTimeout
// 3. Peers discovered before threshold (80%) = founding members → bootstrap together
// 4. Peers discovered after threshold = late joiners → wait for leader to add them
func (c *cluster) handleBootstrap(raftConfig *raft.Config, transport raft.Transport) error {
	// Force bootstrap - skip discovery, single-node cluster
	if c.cfg.forceBootstrap {
		return c.bootstrapSelf(raftConfig, transport)
	}

	// Run discovery and exchange for the full timeout
	ctx, cancel := context.WithTimeout(c.node.Context(), c.cfg.bootstrapTimeout)
	defer cancel()

	// Run discovery in background
	go c.tracker.runDiscoveryAndExchange(ctx, c)

	// Wait for timeout
	<-ctx.Done()

	// Calculate threshold (e.g., 80% of timeout = founding members cutoff)
	threshold := time.Duration(float64(c.cfg.bootstrapTimeout) * c.cfg.bootstrapThreshold)

	// Check if we're a late joiner
	if c.tracker.isLateJoiner(threshold) {
		// We discovered all peers after threshold - we're late
		// Don't bootstrap; request to be added as voter via stream.
		c.requestVoterJoin(5 * time.Second)
		return nil
	}

	// Get founding members (peers discovered before threshold)
	founders := c.tracker.getFoundingMembers(threshold)

	// If we can join an existing cluster, do so and skip bootstrapping.
	if len(founders) > 1 {
		joined, noLeader := c.tryJoinExistingCluster(founders, 5*time.Second)
		if joined {
			return nil
		}
		if !noLeader {
			c.requestVoterJoin(5 * time.Second)
			return nil
		}
	}

	if len(founders) <= 1 {
		if len(c.tracker.allPeers()) == 0 {
			peers := c.raftProtocolPeers()
			if len(peers) > 0 {
				joined, noLeader := c.tryJoinExistingCluster(peers, 5*time.Second)
				if joined {
					return nil
				}
				if !noLeader {
					c.requestVoterJoin(5 * time.Second)
					return nil
				}
			}
		}

		// Only self - bootstrap as single node
		if err := c.bootstrapSelf(raftConfig, transport); err != nil {
			if err == raft.ErrCantBootstrap {
				c.requestVoterJoin(5 * time.Second)
				return nil
			}
			return err
		}
		return nil
	}

	// Bootstrap with all founding members
	if err := c.bootstrapWithPeers(raftConfig, transport, founders); err != nil {
		if err == raft.ErrCantBootstrap {
			c.requestVoterJoin(5 * time.Second)
			return nil
		}
		return err
	}
	return nil
}

// bootstrapWithPeers bootstraps a cluster with the agreed-upon peer list
func (c *cluster) bootstrapWithPeers(raftConfig *raft.Config, transport raft.Transport, peers []peer.ID) error {
	// Build server list from agreed peers
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

	configuration := raft.Configuration{Servers: servers}

	f := c.raft.BootstrapCluster(configuration)
	if err := f.Error(); err != nil {
		return err
	}
	return nil
}

// bootstrapSelf creates a new single-node cluster
func (c *cluster) bootstrapSelf(raftConfig *raft.Config, transport raft.Transport) error {
	configuration := raft.Configuration{
		Servers: []raft.Server{
			{
				ID:       raftConfig.LocalID,
				Address:  transport.LocalAddr(),
				Suffrage: raft.Voter,
			},
		},
	}
	f := c.raft.BootstrapCluster(configuration)
	if err := f.Error(); err != nil {
		return err
	}
	return nil
}

// buildRaftConfig creates the Raft configuration
func (c *cluster) buildRaftConfig() *raft.Config {
	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(c.node.ID().String())

	timeouts := c.cfg.getTimeoutConfig()
	cfg.HeartbeatTimeout = timeouts.HeartbeatTimeout
	cfg.ElectionTimeout = timeouts.ElectionTimeout
	cfg.CommitTimeout = timeouts.CommitTimeout
	cfg.LeaderLeaseTimeout = timeouts.LeaderLeaseTimeout
	cfg.SnapshotInterval = timeouts.SnapshotInterval
	cfg.SnapshotThreshold = timeouts.SnapshotThreshold

	return cfg
}

// createTransport creates the namespace-aware libp2p-based Raft transport
func (c *cluster) createTransport() (raft.Transport, error) {
	timeout := c.cfg.getTimeoutConfig().HeartbeatTimeout
	transport, err := newNamespaceTransport(c.node.Peer(), c.namespace, timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace transport: %w", err)
	}
	return transport, nil
}

// monitorLeadership watches for leadership changes
func (c *cluster) monitorLeadership() {
	for {
		select {
		case isLeader := <-c.raft.LeaderCh():
			select {
			case c.leaderCh <- isLeader:
			default:
				// Channel full, skip
			}
		case <-c.node.Context().Done():
			return
		}
	}
}

// discoverPeers discovers and adds peers from libp2p discovery
func (c *cluster) discoverPeers() {
	ctx := c.node.Context()
	discovery := c.node.Discovery()
	if discovery == nil {
		return
	}

	ticker := time.NewTicker(c.cfg.discoveryConfig.DiscoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if c.closed.Load() {
				return
			}

			// Find peers advertising the same namespace
			peerCh, err := discovery.FindPeers(ctx, c.namespace)
			if err != nil {
				continue
			}

			for p := range peerCh {
				if p.ID == c.node.ID() {
					continue // Skip self
				}
				c.addPeer(p.ID)
			}

		case <-ctx.Done():
			return
		}
	}
}

// addPeer adds a discovered peer to the cluster
func (c *cluster) addPeer(peerID peer.ID) {
	if !c.IsLeader() {
		return // Only leader can add peers
	}

	// NOTE: do not auto-add during discovery; joiners request via stream.
}

func (c *cluster) requestVoterJoin(timeout time.Duration) {
	if c.raftClient == nil || c.tracker == nil {
		return
	}

	// NOTE: join safeguards and retry tuning to be added.
	go func() {
		ctx, cancel := context.WithTimeout(c.node.Context(), 10*time.Second)
		defer cancel()

		go c.tracker.runDiscoveryAndExchange(ctx, c)

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				targets := c.voterJoinTargets()
				for _, pid := range targets {
					if err := c.raftClient.JoinVoter(c.node.ID(), timeout, pid); err != nil {
					} else {
					}
				}
			}
		}
	}()
}

func (c *cluster) tryJoinExistingCluster(peers []peer.ID, timeout time.Duration) (bool, bool) {
	if c.raftClient == nil {
		return false, false
	}

	// NOTE: join safeguards to be added.
	ctx, cancel := context.WithTimeout(c.node.Context(), 2*time.Second)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	sawNoLeader := false

	for {
		select {
		case <-ctx.Done():
			return false, sawNoLeader
		case <-ticker.C:
			for _, pid := range peers {
				if pid == c.node.ID() {
					continue
				}
				if err := c.raftClient.JoinVoter(c.node.ID(), timeout, pid); err == nil {
					return true, false
				} else if errors.Is(err, ErrNoLeader) || strings.Contains(err.Error(), ErrNoLeader.Error()) {
					sawNoLeader = true
				} else {
				}
			}
		}
	}
}

func (c *cluster) voterJoinTargets() []peer.ID {
	targets := make(map[peer.ID]struct{})

	for _, pid := range c.tracker.allPeers() {
		targets[pid] = struct{}{}
	}

	for _, pid := range c.node.Peer().Network().Peers() {
		if pid == c.node.ID() {
			continue
		}
		targets[pid] = struct{}{}
	}

	result := make([]peer.ID, 0, len(targets))
	for pid := range targets {
		result = append(result, pid)
	}
	return result
}

func (c *cluster) raftProtocolPeers() []peer.ID {
	host := c.node.Peer()
	transportProtocol := protocol.ID(TransportProtocol(c.namespace))
	peers := host.Network().Peers()
	filtered := make([]peer.ID, 0, len(peers))

	for _, pid := range peers {
		if pid == c.node.ID() {
			continue
		}
		supported, err := host.Peerstore().SupportsProtocols(pid, transportProtocol)
		if err == nil && len(supported) > 0 {
			filtered = append(filtered, pid)
		}
	}

	return filtered
}

// Close gracefully shuts down the Raft node
func (c *cluster) Close() error {
	if c.closed.Swap(true) {
		return ErrAlreadyClosed
	}

	// Stop stream service
	if c.streamService != nil {
		c.streamService.stop()
	}

	// Close raft client
	if c.raftClient != nil {
		c.raftClient.Close()
	}

	if c.raft != nil {
		return c.raft.Shutdown().Error()
	}

	return nil
}

// Namespace returns the cluster namespace
func (c *cluster) Namespace() string {
	return c.namespace
}

// Set stores a key-value pair
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

// Get retrieves a value by key
func (c *cluster) Get(key string) ([]byte, bool) {
	if c.closed.Load() {
		return nil, false
	}

	if c.fsm == nil {
		return nil, false
	}

	return c.fsm.Get(key)
}

// Delete removes a key
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

// Keys returns all keys matching a prefix
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

// Apply submits raw bytes to be replicated
func (c *cluster) Apply(cmd []byte, timeout time.Duration) (FSMResponse, error) {
	if c.closed.Load() {
		return FSMResponse{}, ErrShutdown
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

// Barrier ensures all preceding operations are committed
func (c *cluster) Barrier(timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	return c.raft.Barrier(timeout).Error()
}

// IsLeader returns true if this node is the current leader
func (c *cluster) IsLeader() bool {
	if c.closed.Load() {
		return false
	}

	return c.raft.State() == raft.Leader
}

// Leader returns the peer ID of the current leader
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

// State returns the current Raft state
func (c *cluster) State() raft.RaftState {
	if c.closed.Load() {
		return raft.Shutdown
	}

	return c.raft.State()
}

// WaitForLeader blocks until a leader is elected
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
		case <-c.node.Context().Done():
			return ErrShutdown
		}
	}
}

// Members returns all cluster members
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

// AddVoter adds a peer as a voting member of the cluster
func (c *cluster) AddVoter(id peer.ID, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	if !c.IsLeader() {
		return ErrNotLeader
	}

	serverID := raft.ServerID(id.String())
	serverAddr := raft.ServerAddress(id.String())

	// Check if already a member
	configFuture := c.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	for _, server := range configFuture.Configuration().Servers {
		if server.ID == serverID {
			return nil // Already a member
		}
	}

	future := c.raft.AddVoter(serverID, serverAddr, 0, timeout)
	if err := future.Error(); err != nil {
		return err
	}
	return nil
}

// RemoveServer removes a node from the cluster
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

// TransferLeadership transfers leadership to another node
func (c *cluster) TransferLeadership() error {
	if c.closed.Load() {
		return ErrShutdown
	}

	return c.raft.LeadershipTransfer().Error()
}

// LeaderCh returns a channel that signals leadership changes
func (c *cluster) LeaderCh() <-chan bool {
	return c.leaderCh
}

// fsmAdapter adapts our FSM interface to raft.FSM
type fsmAdapter struct {
	fsm interface {
		Apply(log *raft.Log) FSMResponse
		Snapshot() (FSMSnapshot, error)
		Restore(io.ReadCloser) error
	}
}

func (a *fsmAdapter) Apply(log *raft.Log) interface{} {
	return a.fsm.Apply(log)
}

func (a *fsmAdapter) Snapshot() (raft.FSMSnapshot, error) {
	snap, err := a.fsm.Snapshot()
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func (a *fsmAdapter) Restore(rc io.ReadCloser) error {
	return a.fsm.Restore(rc)
}
