package raft

import (
	"bytes"
	"context"
	"crypto/cipher"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
)

var clusterLogger = logging.Logger("raft-cluster")

const (
	MaxApplyTimeout = 60 * time.Second

	RaftStoragePrefix = "/raft/"
)

type cluster struct {
	taupeer.Node
	namespace string

	ctx    context.Context
	cancel context.CancelFunc

	timeoutConfig  TimeoutConfig
	forceBootstrap bool
	// ignoreIdentified leaves a peer identified as serving the namespace to the
	// healer's own probes, for a test of what one healer call does.
	ignoreIdentified bool
	quorumLossAfter  time.Duration
	// yielded is set under mu once a yield to another group's leader wiped the
	// member: it joins that group, and no longer forms one of its own however it
	// was made.
	yielded            bool
	bootstrapTimeout   time.Duration
	bootstrapThreshold float64
	encryptionCipher   cipher.AEAD
	clientRetry        rpc.Retry

	// engine is the node's, shared with every group it runs.
	engine *engine

	// The healing settings are read from the package's variables once, in
	// New: a healer stops after Close returns, and still reads them.
	healingProbeInterval      time.Duration
	splitBrainDetectionCycles int

	// member is what initialize builds and a yield takes down and builds
	// again, while the methods, the stream handlers and the healers read it.
	// A reader loads it once and reads the parts of one member.
	member atomic.Pointer[member]

	closed atomic.Bool
	mu     sync.RWMutex
}

// member is one raft and the parts built around it, set before it is
// published and never replaced; the three below say how they differ.
type member struct {
	raft       *group
	fsm        FSM
	tracker    *peerTracker
	raftClient internalClient

	stopTracker context.CancelFunc
	// streamService is set by initialize after the member is published, and
	// read by takeDown: in New, or under the cluster's lock.
	streamService *raftStreamService
	// healer is set once the member has bootstrapped or asked to join.
	healer atomic.Pointer[healer]
	// joined is set once a group lets this member in, which can be before the
	// leader's log, and the configuration in it, reach the member.
	joined atomic.Bool

	// down is closed once takeDown has shut the raft down.
	down     chan struct{}
	downOnce sync.Once
}

// Nodes with the same namespace discover each other.
func New(node taupeer.Node, namespace string, opts ...Option) (Cluster, error) {
	if node == nil {
		return nil, fmt.Errorf("node is required")
	}

	if namespace == "" {
		return nil, fmt.Errorf("%w: namespace cannot be empty", ErrInvalidNamespace)
	}

	c := &cluster{
		Node:               node,
		namespace:          namespace,
		timeoutConfig:      DefaultTimeoutConfig,
		forceBootstrap:     false,
		bootstrapTimeout:   30 * time.Second,
		bootstrapThreshold: 0.8,

		healingProbeInterval:      HealingProbeInterval,
		splitBrainDetectionCycles: SplitBrainDetectionCycles,
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	c.ctx, c.cancel = context.WithCancel(node.Context())
	c.engine = engineFor(node)

	clusterLogger.Infof("[%s] creating raft cluster for namespace %s (bootstrap_timeout=%v, threshold=%.1f%%)",
		node.ID().ShortString(), namespace, c.bootstrapTimeout, c.bootstrapThreshold*100)

	if err := c.initialize(); err != nil {
		c.cancel()
		c.engine.release()
		return nil, err
	}

	return c, nil
}

// initialize takes down what it built when a step fails: New returns no
// cluster to close, and a yield that rebuilds has already taken down the one
// it replaces. The member is published before its stream service listens, so a
// call the new service answers reads the new member.
func (c *cluster) initialize() (err error) {
	fsm := newKVFSM(c.ctx, c.Store(), path.Join(RaftStoragePrefix, c.namespace))
	g, err := c.engine.open(c.namespace, groupConfig{timeouts: c.getTimeoutConfig(), machine: fsm, cipher: c.encryptionCipher, store: c.Store()})
	if err != nil {
		return fmt.Errorf("failed to open raft group: %w", err)
	}
	m := &member{raft: g, fsm: fsm, down: make(chan struct{})}
	defer func() {
		if err != nil {
			m.takeDown()
		}
	}()

	m.raftClient, err = newInternalClient(c.Node, c.namespace, c.encryptionCipher, c.clientRetry)
	if err != nil {
		return fmt.Errorf("failed to create raft client: %w", err)
	}

	m.tracker = newPeerTracker(c.ID())
	var trackerCtx context.Context
	trackerCtx, m.stopTracker = context.WithCancel(c.ctx)

	c.member.Store(m)

	go m.tracker.runDiscoveryAndExchange(trackerCtx, c, m.raftClient)

	m.streamService, err = newStreamService(c)
	if err != nil {
		return fmt.Errorf("failed to create stream service: %w", err)
	}

	if err := c.handleBootstrap(m); err != nil {
		return fmt.Errorf("failed to bootstrap: %w", err)
	}

	h := newHealer(c, m)
	m.raft.Observe(h)
	m.healer.Store(h)
	go h.run(c.ctx)

	clusterLogger.Infof("[%s] raft initialization complete for namespace %s",
		c.ID().ShortString(), c.namespace)

	return nil
}

func (c *cluster) current() *member {
	return c.member.Load()
}

func (c *cluster) handleBootstrap(m *member) error {
	var (
		successfullyCompleted bool
		noLeader              bool
	)
	defer func() {
		if successfullyCompleted {
			m.tracker.setDiscoveryInterval(settledDiscoveryInterval)
			clusterLogger.Infof("[%s] bootstrap completed successfully, discovery interval set to %v",
				c.ID().ShortString(), settledDiscoveryInterval)
		}
	}()

	// A MEMBER CARRYING THE STATE OF A FORMATION THAT LOST ITS QUORUM takes no
	// outside write until that state is in its group, and, holding nothing else,
	// forms the group with it: it was forming it when it stopped.
	if c.hasArchive() {
		m.raft.Freeze(archiveFreeze)
	}
	if c.hasArchive() && m.raft.Pristine() {
		clusterLogger.Infof("[%s] carrying the state of a formation that lost its quorum — forming a group with it", c.ID().ShortString())
		err := c.bootstrapSelf(m)
		if errors.Is(err, ErrCantBootstrap) {
			err = nil
		}
		if err == nil {
			successfullyCompleted = true
		}
		return err
	}

	if c.forceBootstrap && !c.yielded {
		clusterLogger.Infof("[%s] force bootstrap enabled — bootstrapping self", c.ID().ShortString())
		err := c.bootstrapSelf(m)
		// A GROUP IT FORMED BEFORE is the one it resumes, after a restart or a
		// rebuild that kept what it held.
		if errors.Is(err, ErrCantBootstrap) {
			clusterLogger.Infof("[%s] already formed — resuming its group", c.ID().ShortString())
			err = nil
		}
		if err == nil {
			successfullyCompleted = true
		}
		return err
	}

	clusterLogger.Infof("[%s] waiting up to %v for existing cluster to join",
		c.ID().ShortString(), c.bootstrapTimeout)

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
				c.ID().ShortString(), len(peers))
			if successfullyCompleted, _ = c.tryJoinExistingCluster(ctx, m, peers, 1*time.Second); successfullyCompleted {
				clusterLogger.Infof("[%s] joined existing cluster during discovery phase",
					c.ID().ShortString())
				return nil
			}
		}
	}
	clusterLogger.Infof("[%s] bootstrap timeout reached — proceeding to bootstrap decision",
		c.ID().ShortString())

	threshold := time.Duration(float64(c.bootstrapTimeout) * c.bootstrapThreshold)

	if m.tracker.isLateJoiner(threshold) {
		clusterLogger.Infof("[%s] detected as late joiner (all peers seen after %v) — requesting voter join",
			c.ID().ShortString(), threshold)
		c.requestVoterJoin(m, VoterJoinTimeout)
		return nil
	}

	founders := m.tracker.getFoundingMembers(threshold)
	allPeers := m.tracker.livePeers()

	clusterLogger.Infof("[%s] bootstrap decision: %d founding members, %d total peers, threshold=%v",
		c.ID().ShortString(), len(founders), len(allPeers), threshold)

	if len(founders) > 1 {
		clusterLogger.Infof("[%s] multiple founders detected — trying to join existing cluster first",
			c.ID().ShortString())
		if successfullyCompleted, noLeader = c.tryJoinExistingCluster(c.ctx, m, founders, 5*time.Second); successfullyCompleted {
			return nil
		}
		if !noLeader {
			clusterLogger.Infof("[%s] cluster exists but join was rejected — requesting voter join",
				c.ID().ShortString())
			c.requestVoterJoin(m, VoterJoinTimeout)
			return nil
		}
		// Only the lexicographically lowest founder bootstraps; others join.
		if founders[0] != c.ID() {
			clusterLogger.Infof("[%s] not the lowest founder (%s is) — requesting voter join",
				c.ID().ShortString(), founders[0].ShortString())
			c.requestVoterJoin(m, VoterJoinTimeout)
			return nil
		}
		clusterLogger.Infof("[%s] we are the lowest founder — bootstrapping with %d peers",
			c.ID().ShortString(), len(founders))
		if err := c.bootstrapWithPeers(m, founders); err != nil {
			if errors.Is(err, ErrCantBootstrap) {
				clusterLogger.Warnf("[%s] can't bootstrap (already bootstrapped?) — requesting voter join",
					c.ID().ShortString())
				c.requestVoterJoin(m, VoterJoinTimeout)
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
				c.ID().ShortString(), len(peers))
			successfullyCompleted, noLeader = c.tryJoinExistingCluster(c.ctx, m, peers, 5*time.Second)

			if successfullyCompleted {
				return nil
			}
			if !noLeader {
				clusterLogger.Infof("[%s] cluster exists but join was rejected — requesting voter join",
					c.ID().ShortString())
				c.requestVoterJoin(m, VoterJoinTimeout)
				return nil
			}
		}
		clusterLogger.Infof("[%s] no cluster found among peers — requesting late joiner vote",
			c.ID().ShortString())
		c.requestVoterJoin(m, LateJoinerTimeout)
		return nil
	}

	clusterLogger.Infof("[%s] no peers found — bootstrapping as single-node cluster",
		c.ID().ShortString())
	if err := c.bootstrapSelf(m); err != nil {
		if errors.Is(err, ErrCantBootstrap) {
			clusterLogger.Warnf("[%s] can't bootstrap (already bootstrapped?) — requesting voter join",
				c.ID().ShortString())
			c.requestVoterJoin(m, VoterJoinTimeout)
			return nil
		}
		return err
	}
	successfullyCompleted = true
	return nil
}

func (c *cluster) bootstrapWithPeers(m *member, peers []peer.ID) error {
	clusterLogger.Infof("[%s] bootstrapping cluster with %d servers", c.ID().ShortString(), len(peers))
	if err := m.raft.Bootstrap(peers); err != nil {
		clusterLogger.Warnf("[%s] bootstrap failed: %v", c.ID().ShortString(), err)
		return err
	}
	clusterLogger.Infof("[%s] bootstrap succeeded with %d servers", c.ID().ShortString(), len(peers))
	return nil
}

func (c *cluster) bootstrapSelf(m *member) error {
	clusterLogger.Infof("[%s] bootstrapping as single-node", c.ID().ShortString())
	if err := m.raft.Bootstrap([]peer.ID{c.ID()}); err != nil {
		clusterLogger.Warnf("[%s] self-bootstrap failed: %v", c.ID().ShortString(), err)
		return err
	}
	return nil
}

func (c *cluster) getTimeoutConfig() TimeoutConfig {
	if c.timeoutConfig.HeartbeatTimeout > 0 {
		return c.timeoutConfig
	}
	return DefaultTimeoutConfig
}

func (c *cluster) requestVoterJoin(m *member, timeout time.Duration) {
	clusterLogger.Infof("[%s] starting voter join request loop (timeout=%v)", c.ID().ShortString(), timeout)

	go c.askToJoin(m, timeout)
}

// askToJoin asks every 500ms until the group lets m in, however long that takes:
// a member that stopped asking would wait for a healer to notice it. It reads
// only m and stops once m's raft is shut down: a yield builds another member,
// which asks for itself.
func (c *cluster) askToJoin(m *member, timeout time.Duration) {
	ctx := c.ctx

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			clusterLogger.Infof("[%s] voter join request loop ended (context done)", c.ID().ShortString())
			return
		case <-ticker.C:
		}
		if m.raft.State() == Shutdown {
			clusterLogger.Infof("[%s] voter join request loop ended (member taken down)", c.ID().ShortString())
			return
		}
		var targets []peer.ID
		if leader, ok := m.raft.Leader(); ok && leader != c.ID() {
			targets = []peer.ID{leader}
			clusterLogger.Infof("[%s] requesting voter join from leader %s",
				c.ID().ShortString(), leader.ShortString())
		} else {
			targets = c.voterJoinTargets(m.tracker)
			clusterLogger.Infof("[%s] requesting voter join from %d targets (no known leader)",
				c.ID().ShortString(), len(targets))
		}
		if len(targets) == 0 {
			continue
		}
		if !c.bindVia(m, targets) {
			clusterLogger.Infof("[%s] no formation to join among %d targets", c.ID().ShortString(), len(targets))
			continue
		}
		if err := m.raftClient.JoinVoter(ctx, c.ID(), timeout, m.raft.Formation(), m.raft.LastIndex(), targets...); err != nil {
			clusterLogger.Infof("[%s] voter join request failed: %v", c.ID().ShortString(), err)
			continue
		}
		m.joined.Store(true)
		m.tracker.setDiscoveryInterval(settledDiscoveryInterval)
		clusterLogger.Infof("[%s] voter join request accepted", c.ID().ShortString())
		return
	}
}

// It stops once a peer lets this node in, one says there is no leader to ask,
// or two seconds pass, and reports which.
func (c *cluster) tryJoinExistingCluster(ctx context.Context, m *member, peers []peer.ID, timeout time.Duration) (bool, bool) {
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
			clusterLogger.Infof("[%s] tryJoinExistingCluster timed out", c.ID().ShortString())
			return false, false
		}
		var targets []peer.ID
		if leader, ok := m.raft.Leader(); ok && leader != c.ID() {
			targets = []peer.ID{leader}
		} else {
			targets = make([]peer.ID, 0, len(peers))
			for _, pid := range peers {
				if pid != c.ID() {
					targets = append(targets, pid)
				}
			}
		}
		if len(targets) == 0 {
			continue
		}
		if !c.bindVia(m, targets) {
			return false, true
		}
		err := m.raftClient.JoinVoter(ctx, c.ID(), timeout, m.raft.Formation(), m.raft.LastIndex(), targets...)
		if err == nil {
			m.joined.Store(true)
			clusterLogger.Infof("[%s] successfully joined existing cluster", c.ID().ShortString())
			return true, false
		}
		if errors.Is(err, ErrNoLeader) || strings.Contains(err.Error(), ErrNoLeader.Error()) {
			return false, true
		}
	}
}

// archiveFreeze is how long a group carrying an archive takes no outside write:
// until its replay lets them in, which is the end of it in practice.
const archiveFreeze = 365 * 24 * time.Hour

// offerWait is how long a member bound to no formation waits for offers.
const offerWait = 2 * time.Second

// bindVia binds m's group, if it is bound to no formation yet, to one that peers
// offer, and reports whether it is bound. Every node picks the same formation from
// the same offers: one that names it among its voters, then one with a leader, the
// lowest of either; never one with neither.
func (c *cluster) bindVia(m *member, peers []peer.ID) bool {
	if m.raft.Formation() != nil {
		return true
	}
	// THE ANSWERS THAT COME IN TIME: a peer that is gone holds up its own offer, not
	// the join, which asks again on its next round.
	answers := make(chan offer, len(peers))
	asked := 0
	for _, pid := range peers {
		if pid == c.ID() {
			continue
		}
		asked++
		go func() {
			o, err := m.raftClient.Offer(pid)
			if err != nil {
				o = offer{}
			}
			answers <- o
		}()
	}
	deadline := time.After(offerWait)
	var best *offer
collect:
	for range asked {
		select {
		case o := <-answers:
			if len(o.formation) == formationSize && c.rank(o) > 0 && (best == nil || c.prefers(o, *best)) {
				best = &o
			}
		case <-deadline:
			break collect
		}
	}
	if best == nil {
		return false
	}
	err := m.raft.Bind(best.formation)
	switch {
	case err == nil:
		clusterLogger.Infof("[%s] bound to formation %x", c.ID().ShortString(), best.formation)
		return true
	case errors.Is(err, ErrBoundElsewhere):
		return true
	default:
		clusterLogger.Warnf("[%s] binding to formation %x: %v", c.ID().ShortString(), best.formation, err)
		return false
	}
}

// rank is what an offer is worth: one that names this node among its voters, then
// one with a leader to let it in. One with neither is a formation nobody can bring
// this node into, such as a member's still bound to a founder that died, and is
// worth nothing.
func (c *cluster) rank(o offer) int {
	switch {
	case slices.Contains(o.members, c.ID()):
		return 2
	case o.leader != "":
		return 1
	}
	return 0
}

func (c *cluster) prefers(a, b offer) bool {
	if ra, rb := c.rank(a), c.rank(b); ra != rb {
		return ra > rb
	}
	return bytes.Compare(a.formation, b.formation) < 0
}

// Among the connected peers and, given a tracker, those it tracks.
func (c *cluster) protocolPeers(protocolID protocol.ID, tracker *peerTracker) []peer.ID {
	targets := make(map[peer.ID]struct{})

	if tracker != nil {
		for _, pid := range tracker.allPeers() {
			if pid == c.ID() {
				continue
			}
			targets[pid] = struct{}{}
		}
	}

	for _, pid := range c.Host().Network().Peers() {
		if pid == c.ID() {
			continue
		}
		targets[pid] = struct{}{}
	}

	host := c.Host()
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

func (c *cluster) voterJoinTargets(tracker *peerTracker) []peer.ID {
	return c.protocolPeers(protocol.ID(Protocol(c.namespace)), tracker)
}

func (c *cluster) raftProtocolPeers() []peer.ID {
	return c.protocolPeers(protocol.ID(Protocol(c.namespace)), nil)
}

func (c *cluster) Close() error {
	if c.closed.Swap(true) {
		return ErrAlreadyClosed
	}

	clusterLogger.Infof("[%s] closing raft cluster for namespace %s", c.ID().ShortString(), c.namespace)

	if c.cancel != nil {
		c.cancel()
	}

	// A yield takes down and rebuilds the same parts, so the two take turns:
	// a yield in progress finishes, and what it built is what is taken down.
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.current().takeDown()
	c.engine.release()
	return err
}

func (m *member) takeDown() error {
	if m.stopTracker != nil {
		m.stopTracker()
	}

	if m.streamService != nil {
		m.streamService.stop()
	}

	if m.raftClient != nil {
		m.raftClient.Close()
	}

	err := m.raft.Close()
	m.downOnce.Do(func() { close(m.down) })
	return err
}

// holdsNothing says the member would lose nothing leaving its formation: its log
// holds no write, nor its machine anything, tombstones included, which a wipe that
// stopped halfway could leave behind a log it took.
func (m *member) holdsNothing() bool {
	if !m.raft.HoldsNoData() {
		return false
	}
	state, err := m.fsm.ExportState()
	return err == nil && len(state) == 0
}

func (m *member) isLeader() bool {
	return m.raft.State() == Leader
}

func (c *cluster) Namespace() string {
	return c.namespace
}

func (c *cluster) Set(key string, value []byte, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	m := c.current()
	if !m.isLeader() {
		return ErrNotLeader
	}

	cmd, err := encodeSetCommand(key, value)
	if err != nil {
		return fmt.Errorf("failed to encode command: %w", err)
	}

	resp, err := m.raft.Propose(cmd, timeout)
	if err != nil {
		return err
	}
	return resp.Error
}

func (c *cluster) Get(key string) ([]byte, bool) {
	value, found, _ := c.TryGet(key)
	return value, found
}

// TryGet is Get plus why a read came back empty: a closed cluster or one with
// no state machine says so, rather than look like an empty keyspace to a
// caller allocating from absent keys.
func (c *cluster) TryGet(key string) ([]byte, bool, error) {
	if c.closed.Load() {
		return nil, false, ErrShutdown
	}

	m := c.current()
	if m.fsm == nil {
		return nil, false, ErrNoFSM
	}

	return m.fsm.TryGet(key)
}

func (c *cluster) Delete(key string, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	m := c.current()
	if !m.isLeader() {
		return ErrNotLeader
	}

	cmd, err := encodeDeleteCommand(key)
	if err != nil {
		return fmt.Errorf("failed to encode command: %w", err)
	}

	resp, err := m.raft.Propose(cmd, timeout)
	if err != nil {
		return err
	}
	return resp.Error
}

func (c *cluster) Batch(ops []BatchOp, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	m := c.current()
	if !m.isLeader() {
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

	resp, err := m.raft.Propose(data, timeout)
	if err != nil {
		return err
	}
	return resp.Error
}

func (c *cluster) Keys(prefix string) []string {
	if c.closed.Load() {
		return []string{}
	}

	m := c.current()
	if m.fsm == nil {
		return []string{}
	}

	keys := m.fsm.Keys(prefix)
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

	m := c.current()
	if !m.isLeader() {
		return FSMResponse{}, ErrNotLeader
	}

	return m.raft.Propose(cmd, timeout)
}

func (c *cluster) Barrier(timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	// A BARRIER IS THE LEADER'S, as it always was here: a follower's caller is sent
	// to the leader. It costs no log entry: raft's read index, confirmed by a
	// quorum, then applied here.
	m := c.current()
	if !m.isLeader() {
		return ErrNotLeader
	}
	return m.raft.Barrier(timeout)
}

// leadershipMoved is a write that did not land because its leader stopped
// leading: refused (ErrNotLeader), which applied nothing, or lost mid-commit
// (ErrLeadershipLost), which the next leader may still commit.
func leadershipMoved(err error) bool {
	return errors.Is(err, ErrNotLeader) || errors.Is(err, ErrLeadershipLost)
}

func (c *cluster) IsLeader() bool {
	if c.closed.Load() {
		return false
	}

	return c.current().isLeader()
}

func (c *cluster) Leader() (peer.ID, error) {
	if c.closed.Load() {
		return "", ErrShutdown
	}

	return leaderKnownTo(c.current().raft)
}

func leaderKnownTo(g *group) (peer.ID, error) {
	if leader, ok := g.Leader(); ok {
		return leader, nil
	}
	return "", ErrNoLeader
}

func (c *cluster) State() State {
	if c.closed.Load() {
		return Shutdown
	}
	return c.current().raft.State()
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
			if _, ok := c.current().raft.Leader(); ok {
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

	return c.current().members()
}

func (m *member) members() ([]Member, error) {
	return m.raft.Members(), nil
}

func (c *cluster) AddVoter(id peer.ID, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	m := c.current()
	if !m.isLeader() {
		return ErrNotLeader
	}

	for _, member := range m.raft.Members() {
		if member.ID == id {
			clusterLogger.Infof("[%s] peer %s already in configuration — skipping AddVoter",
				c.ID().ShortString(), id.ShortString())
			return nil
		}
	}

	clusterLogger.Infof("[%s] adding voter %s to cluster", c.ID().ShortString(), id.ShortString())
	if err := m.raft.AddVoter(id, timeout); err != nil {
		clusterLogger.Warnf("[%s] AddVoter %s failed: %v", c.ID().ShortString(), id.ShortString(), err)
		return err
	}
	clusterLogger.Infof("[%s] voter %s added successfully", c.ID().ShortString(), id.ShortString())
	return nil
}

// admit lets id in as a voter. One the configuration lists already is left in, but
// a leader holding its progress past last, the end of the log id says it holds,
// hands its lead over: id lost a log it acknowledged, and answers this leader
// nothing more this term (staleLeader). With no voter to hand it to, id is needed
// for the leader's quorum, which its silence ends.
func (c *cluster) admit(id peer.ID, last uint64, timeout time.Duration) error {
	m := c.current()
	if !slices.ContainsFunc(m.raft.Members(), func(mb Member) bool { return mb.ID == id }) {
		return c.AddVoter(id, timeout)
	}
	to, stale := m.raft.standIn(id, last)
	if !stale || to == "" {
		return nil
	}
	clusterLogger.Infof("[%s] %s lost the log this leader holds its progress for — handing the lead to %s",
		c.ID().ShortString(), id.ShortString(), to.ShortString())
	return m.raft.TransferLeadership(to)
}

func (c *cluster) RemoveServer(id peer.ID, timeout time.Duration) error {
	if c.closed.Load() {
		return ErrShutdown
	}

	m := c.current()
	if !m.isLeader() {
		return ErrNotLeader
	}

	clusterLogger.Infof("[%s] removing server %s from cluster", c.ID().ShortString(), id.ShortString())
	if err := m.raft.RemoveServer(id, timeout); err != nil {
		clusterLogger.Warnf("[%s] RemoveServer %s failed: %v", c.ID().ShortString(), id.ShortString(), err)
		return err
	}
	return nil
}

func (c *cluster) TransferLeadership() error {
	if c.closed.Load() {
		return ErrShutdown
	}

	clusterLogger.Infof("[%s] transferring leadership", c.ID().ShortString())
	return c.current().raft.TransferLeadership("")
}
