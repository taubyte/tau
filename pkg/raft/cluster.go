package raft

import (
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/raft"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	taupeer "github.com/taubyte/tau/p2p/peer"
)

var clusterLogger = logging.Logger("raft-cluster")

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
	stopTracker   context.CancelFunc
	streamService *raftStreamService
	raftClient    internalClient // Client for remote Raft operations (joining voters, forwarding to leader, etc.)
	healer        *healer

	snapshotDir string

	closed atomic.Bool
	// joined is set once a group lets this member in, which can be before the
	// leader's log, and the configuration in it, reach the member.
	joined atomic.Bool
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
		timeoutConfig:      DefaultTimeoutConfig,
		forceBootstrap:     false,
		bootstrapTimeout:   30 * time.Second,
		bootstrapThreshold: 0.8,
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	c.ctx, c.cancel = context.WithCancel(node.Context())

	clusterLogger.Infof("[%s] creating raft cluster for namespace %s (bootstrap_timeout=%v, threshold=%.1f%%)",
		node.ID().ShortString(), namespace, c.bootstrapTimeout, c.bootstrapThreshold*100)

	if err := c.initialize(); err != nil {
		c.cancel()
		return nil, err
	}

	return c, nil
}

// initialize builds the group's member, and takes down what it built when a
// step fails: New returns no cluster to close, and a yield that rebuilds has
// already taken down the one it replaces.
func (c *cluster) initialize() (err error) {
	store := c.node.Store()
	storagePrefix := path.Join(RaftStoragePrefix, c.namespace)

	clusterLogger.Infof("[%s] initializing raft (storage_prefix=%s)", c.node.ID().ShortString(), storagePrefix)

	c.logStore = newLogStore(store, path.Join(storagePrefix, "log"))
	c.stable = newStableStore(store, path.Join(storagePrefix, "stable"))

	snapDir := c.snapshotDir
	if snapDir == "" {
		folder, err := c.node.NewFolder(path.Join("raft", strings.ReplaceAll(c.namespace, "/", "_")))
		if err != nil {
			return fmt.Errorf("failed to create snapshot store: %w", err)
		}
		snapDir = folder.Path()
	}
	c.snaps, err = newSnapshotStore(snapDir, defaultRetainSnapshots)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	c.fsm = newKVFSM(c.ctx, store, storagePrefix)

	raftConfig := c.buildRaftConfig()

	transport, err := c.createTransport()
	if err != nil {
		return fmt.Errorf("failed to create transport: %w", err)
	}

	timeouts := c.getTimeoutConfig()
	clusterLogger.Infof("[%s] raft config: heartbeat=%v election=%v commit=%v lease=%v snap_interval=%v snap_threshold=%d prevote=enabled",
		c.node.ID().ShortString(),
		timeouts.HeartbeatTimeout, timeouts.ElectionTimeout,
		timeouts.CommitTimeout, timeouts.LeaderLeaseTimeout,
		timeouts.SnapshotInterval, timeouts.SnapshotThreshold)

	r, err := raft.NewRaft(raftConfig, c.fsm, c.logStore, c.stable, c.snaps, transport)
	if err != nil {
		transport.Close()
		return fmt.Errorf("failed to create raft: %w", err)
	}
	c.raft = r
	defer func() {
		if err != nil {
			c.takeDown()
		}
	}()

	c.raftClient, err = newInternalClient(c.node, c.namespace, c.encryptionCipher)
	if err != nil {
		return fmt.Errorf("failed to create raft client: %w", err)
	}

	c.tracker = newPeerTracker(c.node.ID())

	var trackerCtx context.Context
	trackerCtx, c.stopTracker = context.WithCancel(c.ctx)
	go c.tracker.runDiscoveryAndExchange(trackerCtx, c)

	c.streamService, err = newStreamService(c)
	if err != nil {
		return fmt.Errorf("failed to create stream service: %w", err)
	}

	if err := c.handleBootstrap(raftConfig, transport); err != nil {
		return fmt.Errorf("failed to bootstrap: %w", err)
	}

	c.healer = newHealer(c)
	c.healer.registerObserver()
	go c.healer.run(c.ctx)

	clusterLogger.Infof("[%s] raft initialization complete for namespace %s",
		c.node.ID().ShortString(), c.namespace)

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
			c.tracker.setDiscoveryInterval(settledDiscoveryInterval)
			clusterLogger.Infof("[%s] bootstrap completed successfully, discovery interval set to %v",
				c.node.ID().ShortString(), settledDiscoveryInterval)
		}
	}()

	if c.forceBootstrap {
		clusterLogger.Infof("[%s] force bootstrap enabled — bootstrapping self", c.node.ID().ShortString())
		err := c.bootstrapSelf(raftConfig, transport)
		if err == nil {
			successfullyCompleted = true
		}
		return err
	}

	clusterLogger.Infof("[%s] waiting up to %v for existing cluster to join",
		c.node.ID().ShortString(), c.bootstrapTimeout)

	ctx, cancel := context.WithTimeout(c.ctx, c.bootstrapTimeout)
	defer cancel()
	ticker := time.NewTicker(BootstrapCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
		// A tick and the deadline can be ready together, and select takes
		// either: the deadline wins, and no join starts after it.
		if ctx.Err() != nil {
			break
		}
		peers := c.raftProtocolPeers()
		if len(peers) > 0 {
			clusterLogger.Infof("[%s] found %d raft-protocol peers — attempting join",
				c.node.ID().ShortString(), len(peers))
			if successfullyCompleted, _ = c.tryJoinExistingCluster(ctx, peers, 1*time.Second); successfullyCompleted {
				clusterLogger.Infof("[%s] joined existing cluster during discovery phase",
					c.node.ID().ShortString())
				return nil
			}
		}
	}
	clusterLogger.Infof("[%s] bootstrap timeout reached — proceeding to bootstrap decision",
		c.node.ID().ShortString())

	threshold := time.Duration(float64(c.bootstrapTimeout) * c.bootstrapThreshold)

	if c.tracker.isLateJoiner(threshold) {
		clusterLogger.Infof("[%s] detected as late joiner (all peers seen after %v) — requesting voter join",
			c.node.ID().ShortString(), threshold)
		c.requestVoterJoin(VoterJoinTimeout)
		return nil
	}

	founders := c.tracker.getFoundingMembers(threshold)
	allPeers := c.tracker.allPeers()

	clusterLogger.Infof("[%s] bootstrap decision: %d founding members, %d total peers, threshold=%v",
		c.node.ID().ShortString(), len(founders), len(allPeers), threshold)

	if len(founders) > 1 {
		clusterLogger.Infof("[%s] multiple founders detected — trying to join existing cluster first",
			c.node.ID().ShortString())
		if successfullyCompleted, noLeader = c.tryJoinExistingCluster(c.ctx, founders, 5*time.Second); successfullyCompleted {
			return nil
		}
		if !noLeader {
			clusterLogger.Infof("[%s] cluster exists but join was rejected — requesting voter join",
				c.node.ID().ShortString())
			c.requestVoterJoin(VoterJoinTimeout)
			return nil
		}
		// Only the lexicographically lowest founder bootstraps; others join.
		if founders[0] != c.node.ID() {
			clusterLogger.Infof("[%s] not the lowest founder (%s is) — requesting voter join",
				c.node.ID().ShortString(), founders[0].ShortString())
			c.requestVoterJoin(VoterJoinTimeout)
			return nil
		}
		clusterLogger.Infof("[%s] we are the lowest founder — bootstrapping with %d peers",
			c.node.ID().ShortString(), len(founders))
		if err := c.bootstrapWithPeers(transport, founders); err != nil {
			if err == raft.ErrCantBootstrap {
				clusterLogger.Warnf("[%s] can't bootstrap (already bootstrapped?) — requesting voter join",
					c.node.ID().ShortString())
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
			clusterLogger.Infof("[%s] single founder with %d raft-protocol peers — attempting join",
				c.node.ID().ShortString(), len(peers))
			successfullyCompleted, noLeader = c.tryJoinExistingCluster(c.ctx, peers, 5*time.Second)

			if successfullyCompleted {
				return nil
			}
			if !noLeader {
				clusterLogger.Infof("[%s] cluster exists but join was rejected — requesting voter join",
					c.node.ID().ShortString())
				c.requestVoterJoin(VoterJoinTimeout)
				return nil
			}
		}
		clusterLogger.Infof("[%s] no cluster found among peers — requesting late joiner vote",
			c.node.ID().ShortString())
		c.requestVoterJoin(LateJoinerTimeout)
		return nil
	}

	clusterLogger.Infof("[%s] no peers found — bootstrapping as single-node cluster",
		c.node.ID().ShortString())
	if err := c.bootstrapSelf(raftConfig, transport); err != nil {
		if err == raft.ErrCantBootstrap {
			clusterLogger.Warnf("[%s] can't bootstrap (already bootstrapped?) — requesting voter join",
				c.node.ID().ShortString())
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

	clusterLogger.Infof("[%s] bootstrapping cluster with %d servers", c.node.ID().ShortString(), len(servers))
	for i, s := range servers {
		clusterLogger.Infof("[%s]   server[%d]: id=%s addr=%s", c.node.ID().ShortString(), i, s.ID, s.Address)
	}

	f := c.raft.BootstrapCluster(raft.Configuration{Servers: servers})
	if err := f.Error(); err != nil {
		clusterLogger.Warnf("[%s] bootstrap failed: %v", c.node.ID().ShortString(), err)
		return err
	}

	clusterLogger.Infof("[%s] bootstrap succeeded with %d servers", c.node.ID().ShortString(), len(servers))
	return nil
}

func (c *cluster) bootstrapSelf(raftConfig *raft.Config, transport raft.Transport) error {
	clusterLogger.Infof("[%s] bootstrapping as single-node (id=%s addr=%s)",
		c.node.ID().ShortString(), raftConfig.LocalID, transport.LocalAddr())

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
		clusterLogger.Warnf("[%s] self-bootstrap failed: %v", c.node.ID().ShortString(), err)
		return err
	}
	return nil
}

func (c *cluster) buildRaftConfig() *raft.Config {
	cfg := raft.DefaultConfig()
	cfg.LocalID = raft.ServerID(c.node.ID().String())
	cfg.PreVoteDisabled = false
	// A leader that commits its own removal steps down instead of shutting
	// itself down. raft drops the future of a shutdown it starts itself, so
	// Close would find it already shut down, get a future that waits for
	// nothing, and return with its goroutines still writing to the store.
	cfg.ShutdownOnRemove = false

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
	return DefaultTimeoutConfig
}

// minTransportTimeout floors the transport I/O deadline so it is never tied to
// an aggressive HeartbeatTimeout. A heartbeat as low as tens of ms would make
// every RPC (a dial + libp2p stream negotiation on a cold peer) race that same
// budget; raft-level liveness stays fast via HeartbeatTimeout/ElectionTimeout
// regardless. No effect on production (default HeartbeatTimeout is 15s).
const minTransportTimeout = time.Second

func (c *cluster) createTransport() (*raft.NetworkTransport, error) {
	timeout := max(c.getTimeoutConfig().HeartbeatTimeout, minTransportTimeout)
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

	clusterLogger.Infof("[%s] starting voter join request loop (timeout=%v)", c.node.ID().ShortString(), timeout)

	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer cancel()

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				clusterLogger.Infof("[%s] voter join request loop ended (context done)", c.node.ID().ShortString())
				return
			case <-ticker.C:
				var targets []peer.ID
				if leader, err := c.Leader(); err == nil && leader != c.node.ID() {
					targets = []peer.ID{leader}
					clusterLogger.Infof("[%s] requesting voter join from leader %s",
						c.node.ID().ShortString(), leader.ShortString())
				} else {
					targets = c.voterJoinTargets()
					clusterLogger.Infof("[%s] requesting voter join from %d targets (no known leader)",
						c.node.ID().ShortString(), len(targets))
				}
				if len(targets) > 0 {
					if err := c.raftClient.JoinVoter(ctx, c.node.ID(), timeout, targets...); err != nil {
						clusterLogger.Infof("[%s] voter join request failed: %v", c.node.ID().ShortString(), err)
					} else {
						c.joined.Store(true)
						c.tracker.setDiscoveryInterval(settledDiscoveryInterval)
						clusterLogger.Infof("[%s] voter join request accepted", c.node.ID().ShortString())
					}
				}
			}
		}
	}()
}

// tryJoinExistingCluster asks peers to let this node in until one does, one
// says there is no leader to ask, or two seconds pass, within ctx. It reports
// whether the node joined and whether it was told there is no leader.
func (c *cluster) tryJoinExistingCluster(ctx context.Context, peers []peer.ID, timeout time.Duration) (bool, bool) {
	if c.raftClient == nil {
		return false, false
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
		if ctx.Err() != nil {
			clusterLogger.Infof("[%s] tryJoinExistingCluster timed out", c.node.ID().ShortString())
			return false, false
		}
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
		err := c.raftClient.JoinVoter(ctx, c.node.ID(), timeout, targets...)
		if err == nil {
			c.joined.Store(true)
			clusterLogger.Infof("[%s] successfully joined existing cluster", c.node.ID().ShortString())
			return true, false
		}
		if errors.Is(err, ErrNoLeader) || strings.Contains(err.Error(), ErrNoLeader.Error()) {
			return false, true
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

// raftProtocolPeers are the connected peers listed as serving the group's
// protocol, the one a join is asked over.
func (c *cluster) raftProtocolPeers() []peer.ID {
	return c.protocolPeers(protocol.ID(Protocol(c.namespace)), false)
}

// Close gracefully shuts down the Raft node
func (c *cluster) Close() error {
	if c.closed.Swap(true) {
		return ErrAlreadyClosed
	}

	clusterLogger.Infof("[%s] closing raft cluster for namespace %s", c.node.ID().ShortString(), c.namespace)

	if c.cancel != nil {
		c.cancel()
	}

	// A yield takes down and rebuilds the same parts, so the two take turns:
	// a yield in progress finishes, and what it built is what is taken down.
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.takeDown()
}

// takeDown stops everything initialize builds and waits for the raft.
func (c *cluster) takeDown() error {
	if c.stopTracker != nil {
		c.stopTracker()
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
		return mapNotLeader(err)
	}

	resp := future.Response()
	if fsmResp, ok := resp.(FSMResponse); ok && fsmResp.Error != nil {
		return fsmResp.Error
	}

	return nil
}

func (c *cluster) Get(key string) ([]byte, bool) {
	value, found, _ := c.TryGet(key)
	return value, found
}

// TryGet answers as Get does, plus the reason a read came back empty.
//
// A CLUSTER THAT CANNOT READ SAYS SO RATHER THAN SAYING THE KEY IS FREE. A
// closed cluster and a cluster with no state machine both answered false
// through Get, which a caller allocating from an absent key reads as an empty
// keyspace.
func (c *cluster) TryGet(key string) ([]byte, bool, error) {
	if c.closed.Load() {
		return nil, false, ErrShutdown
	}

	if c.fsm == nil {
		return nil, false, ErrNoFSM
	}

	return c.fsm.TryGet(key)
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
		return mapNotLeader(err)
	}

	resp := future.Response()
	if fsmResp, ok := resp.(FSMResponse); ok && fsmResp.Error != nil {
		return fsmResp.Error
	}

	return nil
}

func (c *cluster) Batch(ops []BatchOp, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	if !c.IsLeader() {
		return ErrNotLeader
	}

	cmds := make([]Command, 0, len(ops))
	for _, op := range ops {
		switch {
		case op.Set != nil:
			cmds = append(cmds, Command{Type: CommandSet, Set: op.Set})
		case op.Delete != nil:
			cmds = append(cmds, Command{Type: CommandDelete, Delete: op.Delete})
		default:
			return ErrInvalidCommand
		}
	}

	data, err := encodeBatchCommand(cmds)
	if err != nil {
		return fmt.Errorf("failed to encode batch command: %w", err)
	}

	future := c.raft.Apply(data, timeout)
	if err := future.Error(); err != nil {
		return mapNotLeader(err)
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
		return FSMResponse{}, mapNotLeader(err)
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

	return mapNotLeader(c.raft.Barrier(timeout).Error())
}

// mapNotLeader answers raft's refusal of a member that does not lead as
// ErrNotLeader, which is the error callers test for.
func mapNotLeader(err error) error {
	if errors.Is(err, raft.ErrNotLeader) {
		return ErrNotLeader
	}
	return err
}

// leadershipMoved is a write that did not land because its leader stopped
// leading: refused (ErrNotLeader), which applied nothing, or lost mid-commit
// (raft.ErrLeadershipLost), which the next leader may still commit.
func leadershipMoved(err error) bool {
	return errors.Is(err, ErrNotLeader) || errors.Is(err, raft.ErrLeadershipLost)
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
			clusterLogger.Infof("[%s] peer %s already in configuration — skipping AddVoter",
				c.node.ID().ShortString(), id.ShortString())
			return nil
		}
	}

	clusterLogger.Infof("[%s] adding voter %s to cluster", c.node.ID().ShortString(), id.ShortString())

	future := c.raft.AddVoter(serverID, serverAddr, 0, timeout)
	if err := future.Error(); err != nil {
		clusterLogger.Warnf("[%s] AddVoter %s failed: %v", c.node.ID().ShortString(), id.ShortString(), err)
		return mapNotLeader(err)
	}

	clusterLogger.Infof("[%s] voter %s added successfully", c.node.ID().ShortString(), id.ShortString())
	return nil
}

func (c *cluster) RemoveServer(id peer.ID, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	if !c.IsLeader() {
		return ErrNotLeader
	}

	clusterLogger.Infof("[%s] removing server %s from cluster", c.node.ID().ShortString(), id.ShortString())

	serverID := raft.ServerID(id.String())
	future := c.raft.RemoveServer(serverID, 0, timeout)
	if err := future.Error(); err != nil {
		clusterLogger.Warnf("[%s] RemoveServer %s failed: %v", c.node.ID().ShortString(), id.ShortString(), err)
		return mapNotLeader(err)
	}
	return nil
}

func (c *cluster) TransferLeadership() error {
	if c.closed.Load() {
		return ErrShutdown
	}

	clusterLogger.Infof("[%s] transferring leadership", c.node.ID().ShortString())
	return mapNotLeader(c.raft.LeadershipTransfer().Error())
}
