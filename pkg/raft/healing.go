package raft

import (
	"context"
	"path"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

var healLogger = logging.Logger("raft-healing")

type healer struct {
	cluster *cluster
	member  *member

	healAckCh     chan peer.ID
	foreignVoteCh chan peer.ID
	healing       atomic.Bool
	mu            sync.Mutex
	superseded    atomic.Bool
	// leaderChangedAt is when raft last reported this member gaining or losing a
	// leader, in unix nanoseconds: up to then it knew one.
	leaderChangedAt atomic.Int64
}

func newHealer(c *cluster, m *member) *healer {
	return &healer{
		cluster:       c,
		member:        m,
		healAckCh:     make(chan peer.ID, 1),
		foreignVoteCh: make(chan peer.ID, 16),
	}
}

func (h *healer) signalHealAck(from peer.ID) {
	select {
	case h.healAckCh <- from:
	default:
	}
}

// Never blocks raft: a full channel drops the report.
func (h *healer) notifyForeignVote(pid peer.ID) {
	select {
	case h.foreignVoteCh <- pid:
	default:
	}
}

func (h *healer) run(ctx context.Context) {
	electionTimeout := h.cluster.getTimeoutConfig().ElectionTimeout
	if electionTimeout <= 0 {
		electionTimeout = 10 * time.Second
	}

	checkInterval := electionTimeout * 2
	if checkInterval < h.cluster.healingProbeInterval {
		checkInterval = h.cluster.healingProbeInterval
	}

	healLogger.Infof("[%s] healer started (check_interval=%v, detection_cycles=%d)",
		h.cluster.ID().ShortString(), checkInterval, h.cluster.splitBrainDetectionCycles)

	noLeaderCycles := 0
	cycleStart := time.Now()
	// ONE TICKER FOR THE LIFE OF THE HEALER: a timer armed afresh after each event
	// would let a steady stream of foreign messages put every check off forever.
	checks := time.NewTicker(checkInterval)
	defer checks.Stop()

	for {
		select {
		case <-ctx.Done():
			healLogger.Infof("[%s] healer stopped (context cancelled)", h.cluster.ID().ShortString())
			return
		case pid := <-h.foreignVoteCh:
			h.handleForeignVote(ctx, pid)
			continue
		case from := <-h.healAckCh:
			h.healAcked(ctx, from)
			if h.superseded.Load() {
				healLogger.Infof("[%s] healer superseded after yieldAndRejoin — exiting", h.cluster.ID().ShortString())
				return
			}
			noLeaderCycles = 0
			continue
		case <-checks.C:
		}

		if h.cluster.closed.Load() || h.superseded.Load() {
			healLogger.Infof("[%s] healer stopped (closed=%v superseded=%v)",
				h.cluster.ID().ShortString(), h.cluster.closed.Load(), h.superseded.Load())
			return
		}

		if h.cluster.IsLeader() {
			noLeaderCycles = 0
			cycleStart = time.Now()
			h.probeAndMergeAsLeader(ctx)
			continue
		}

		hadLeader := h.hadLeaderSince(cycleStart)
		cycleStart = time.Now()
		if hadLeader {
			noLeaderCycles = 0
			continue
		}

		noLeaderCycles++
		healLogger.Infof("[%s] no leader detected (cycle %d/%d, state=%s)",
			h.cluster.ID().ShortString(), noLeaderCycles, h.cluster.splitBrainDetectionCycles,
			h.member.raft.State())

		if noLeaderCycles < h.cluster.splitBrainDetectionCycles {
			continue
		}

		healLogger.Warnf("[%s] split-brain suspected: %d consecutive leaderless cycles",
			h.cluster.ID().ShortString(), noLeaderCycles)

		h.detectAndHeal(ctx)
		if h.superseded.Load() {
			healLogger.Infof("[%s] healer superseded after detectAndHeal — exiting", h.cluster.ID().ShortString())
			return
		}
		noLeaderCycles = 0
	}
}

// healAcked yields to the group whose leader sent the healAck, unless that
// leader is this member's own: a healAck sent while the member was still
// joining, or before it rejoined, lands on a member of the sender's own group,
// which has nothing to yield, and yielding would wipe it.
func (h *healer) healAcked(ctx context.Context, from peer.ID) {
	if leader, err := h.cluster.Leader(); err == nil && leader == from {
		healLogger.Infof("[%s] healAck from %s, which leads us — nothing to yield",
			h.cluster.ID().ShortString(), from.ShortString())
		return
	}
	healLogger.Infof("[%s] received healAck from %s — yielding immediately",
		h.cluster.ID().ShortString(), from.ShortString())
	h.yieldAndRejoin(ctx, from)
}

// hadLeaderSince reports whether this member knew a leader at any moment since
// t. A probe that finds no leader may have caught an election: a group whose
// leader steps down and is replaced between probes is not split, and only a
// cycle with no leader in it counts toward split-brain handling.
func (h *healer) hadLeaderSince(t time.Time) bool {
	if _, err := h.cluster.Leader(); err == nil {
		return true
	}
	return h.leaderChangedAt.Load() >= t.UnixNano()
}

// The peer is tracked so findForeignPeers finds it, and a leader absorbs it at
// once.
func (h *healer) handleForeignVote(ctx context.Context, pid peer.ID) {
	if h.cluster.closed.Load() {
		return
	}

	h.member.tracker.addPeer(pid)

	if !h.cluster.IsLeader() {
		healLogger.Infof("[%s] tracked foreign voter %s (we are %s, not leader)",
			h.cluster.ID().ShortString(), pid.ShortString(), h.member.raft.State())
		return
	}

	healLogger.Infof("[%s] vote request from non-member %s — absorbing as leader",
		h.cluster.ID().ShortString(), pid.ShortString())
	h.absorbOrphan(ctx, pid)
}

// leaderChanged and foreignVote are the engine telling the healer what it watches
// for, from the engine's loop: neither blocks.
func (h *healer) leaderChanged(peer.ID) {
	h.leaderChangedAt.Store(time.Now().UnixNano())
}

func (h *healer) foreignVote(pid peer.ID) {
	if pid == h.cluster.ID() {
		return
	}
	healLogger.Infof("[%s] observed vote request from non-member %s",
		h.cluster.ID().ShortString(), pid.ShortString())
	h.notifyForeignVote(pid)
}

func (h *healer) probeAndMergeAsLeader(ctx context.Context) {
	foreignPeers := h.findForeignPeers()
	if len(foreignPeers) == 0 {
		return
	}

	healLogger.Infof("[%s] probing %d foreign peers as leader", h.cluster.ID().ShortString(), len(foreignPeers))

	for _, pid := range foreignPeers {
		info, err := h.member.raftClient.ClusterInfo(pid)
		if err != nil {
			healLogger.Infof("[%s] clusterInfo from foreign %s failed: %v",
				h.cluster.ID().ShortString(), pid.ShortString(), err)
			continue
		}

		if info.LeaderID == "" {
			// A peer in no configuration belongs to no group: it is joining
			// one, and its join, not a healAck, is what adds it.
			if info.MemberCount == 0 {
				continue
			}
			healLogger.Infof("[%s] detected orphaned foreign node %s — absorbing",
				h.cluster.ID().ShortString(), pid.ShortString())
			h.absorbOrphan(ctx, pid)
			continue
		}

		localInfo := h.localClusterInfo()
		winner := negotiateWinner(localInfo, info)

		healLogger.Infof("[%s] foreign %s: leader=%s members=%d lastIndex=%d → winner=%s",
			h.cluster.ID().ShortString(), pid.ShortString(),
			info.LeaderID, info.MemberCount, info.LastIndex, winner)

		if winner == localInfo.NodeID {
			healLogger.Infof("[%s] detected foreign cluster led by %s — we win, initiating merge",
				h.cluster.ID().ShortString(), info.LeaderID)

			leaderPID, err := peer.Decode(info.LeaderID)
			if err != nil {
				continue
			}
			h.executeMerge(ctx, leaderPID)
		}
	}
}

func (h *healer) detectAndHeal(ctx context.Context) {
	if !h.healing.CompareAndSwap(false, true) {
		healLogger.Infof("[%s] detectAndHeal skipped: already healing", h.cluster.ID().ShortString())
		return
	}
	defer h.healing.Store(false)

	// A MEMBER THAT HOLDS NOTHING FORMS AGAIN, with whoever is there or alone: the
	// formation it is bound to never reached it, and may never, if the member that
	// formed it is gone. Leaving it loses nothing.
	pristine := h.member.raft.Pristine()
	foreignPeers := h.findForeignPeers()
	if len(foreignPeers) == 0 {
		if pristine {
			healLogger.Warnf("[%s] leaderless and holding nothing — forming again", h.cluster.ID().ShortString())
			h.yieldAndRejoin(ctx, peer.ID(""))
			return
		}
		healLogger.Infof("[%s] detectAndHeal: no foreign peers found", h.cluster.ID().ShortString())
		return
	}

	healLogger.Infof("[%s] detectAndHeal: found %d foreign peers", h.cluster.ID().ShortString(), len(foreignPeers))

	var foreignInfo *ClusterInfoResponse
	var foreignLeader peer.ID
	for _, pid := range foreignPeers {
		info, err := h.member.raftClient.ClusterInfo(pid)
		if err != nil {
			healLogger.Infof("[%s] detectAndHeal: clusterInfo from %s failed: %v",
				h.cluster.ID().ShortString(), pid.ShortString(), err)
			continue
		}
		foreignInfo = info
		if info.LeaderID != "" {
			foreignLeader, _ = peer.Decode(info.LeaderID)
		}
		break
	}

	if foreignInfo == nil {
		healLogger.Infof("[%s] detectAndHeal: could not reach any foreign peer", h.cluster.ID().ShortString())
		return
	}

	localInfo := h.localClusterInfo()
	winner := negotiateWinner(localInfo, foreignInfo)

	healLogger.Infof("[%s] detectAndHeal: local(leader=%s, members=%d, lastIndex=%d) vs foreign(leader=%s, members=%d, lastIndex=%d) → winner=%s",
		h.cluster.ID().ShortString(),
		localInfo.LeaderID, localInfo.MemberCount, localInfo.LastIndex,
		foreignInfo.LeaderID, foreignInfo.MemberCount, foreignInfo.LastIndex,
		winner)

	if foreignLeader == "" {
		// NOBODY WIPES WHAT IT HOLDS FOR A PEER WITH NO LEADER: that peer may hold
		// nothing at all, a newcomer, and a member of a group that lost its quorum
		// would throw away the only copy of what the group committed. Only a member
		// holding nothing forms again, with the rest.
		if !pristine {
			healLogger.Warnf("[%s] both sides leaderless — holding state, not wiping it",
				h.cluster.ID().ShortString())
			return
		}
		healLogger.Warnf("[%s] both sides leaderless and this member holds nothing — forming again",
			h.cluster.ID().ShortString())
		h.yieldAndRejoin(ctx, peer.ID(""))
		return
	}

	if winner == localInfo.NodeID {
		return
	}

	healLogger.Infof("[%s] we lose to %s — yielding",
		h.cluster.ID().ShortString(), foreignInfo.LeaderID)

	h.yieldAndRejoin(ctx, foreignLeader)
}

// findForeignPeers returns the tracked peers that serve our protocol but are
// not raft members. One the peerstore does not list is probed: what identify
// recorded can be stale, and a member a group removed would never be absorbed.
func (h *healer) findForeignPeers() []peer.ID {
	host := h.cluster.Host()
	raftProtocol := protocol.ID(Protocol(h.cluster.namespace))

	members, err := h.member.members()
	if err != nil {
		healLogger.Infof("[%s] findForeignPeers: cannot get members: %v",
			h.cluster.ID().ShortString(), err)
		return nil
	}
	memberSet := make(map[peer.ID]struct{}, len(members))
	for _, m := range members {
		memberSet[m.ID] = struct{}{}
	}

	allTracked := h.member.tracker.allPeers()

	var foreign []peer.ID
	for _, pid := range allTracked {
		if _, inConfig := memberSet[pid]; inConfig {
			continue
		}
		if supportsRaftProtocol(host, pid, raftProtocol) || h.member.tracker.probe(h.cluster.ctx, host, pid, raftProtocol) {
			foreign = append(foreign, pid)
		}
	}

	if len(foreign) > 0 {
		healLogger.Infof("[%s] findForeignPeers: %d foreign out of %d tracked (%d members in config)",
			h.cluster.ID().ShortString(), len(foreign), len(allTracked), len(members))
	}

	return foreign
}

func (h *healer) localClusterInfo() *ClusterInfoResponse {
	info := &ClusterInfoResponse{
		NodeID: h.cluster.ID().String(),
	}
	if leader, err := leaderKnownTo(h.member.raft); err == nil {
		info.LeaderID = leader.String()
	}
	info.Term = h.member.raft.Term()
	info.LastIndex = h.member.raft.LastIndex()
	if members, err := h.member.members(); err == nil {
		info.MemberCount = len(members)
	}
	return info
}

func negotiateWinner(a, b *ClusterInfoResponse) string {
	if a.MemberCount != b.MemberCount {
		if a.MemberCount > b.MemberCount {
			return a.NodeID
		}
		return b.NodeID
	}
	if a.LastIndex != b.LastIndex {
		if a.LastIndex > b.LastIndex {
			return a.NodeID
		}
		return b.NodeID
	}
	aLeader := a.LeaderID
	if aLeader == "" {
		aLeader = a.NodeID
	}
	bLeader := b.LeaderID
	if bLeader == "" {
		bLeader = b.NodeID
	}
	if aLeader < bLeader {
		return a.NodeID
	}
	return b.NodeID
}

func mergeCRDTDelta(ourState, foreignState map[string]CRDTEntry) map[string]CRDTEntry {
	delta := make(map[string]CRDTEntry)
	for key, foreign := range foreignState {
		local, exists := ourState[key]
		if !exists || crdtEntryWins(foreign, local) {
			delta[key] = foreign
		}
	}
	return delta
}

func warnForeignWallClockDrift(self peer.ID, foreign map[string]CRDTEntry) {
	now := time.Now().UnixNano()
	limit := MaxWallClockDrift.Nanoseconds()
	bad := 0
	for _, e := range foreign {
		d := now - e.WallClock
		if d > limit || d < -limit {
			bad++
		}
	}
	if bad > 0 {
		healLogger.Warnf("[%s] %d foreign FSM entries exceed wall-clock drift %v (tiebreaker unreliable); sync NTP",
			self.ShortString(), bad, MaxWallClockDrift)
	}
}

// absorbOrphan merges a leaderless foreign node's state, then sends it a
// healAck so it wipes its raft state and asks to join. AddVoter waits for that
// join: added while the orphan still ran a conflicting raft, it would
// destabilise this group with its term.
func (h *healer) absorbOrphan(ctx context.Context, orphan peer.ID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.cluster.IsLeader() {
		return
	}

	// A yield wipes the only copy of what the orphan holds, so it is told to
	// yield only once all of that has landed here.
	foreignState, _, err := h.member.raftClient.ExportFSM(orphan)
	if err != nil {
		healLogger.Warnf("[%s] export FSM from orphan %s failed: %v",
			h.cluster.ID().ShortString(), orphan.ShortString(), err)
		return
	}
	if len(foreignState) > 0 {
		ourState, err := h.member.fsm.ExportState()
		if err != nil {
			healLogger.Errorf("[%s] export local FSM failed: %v", h.cluster.ID().ShortString(), err)
			return
		}
		warnForeignWallClockDrift(h.cluster.ID(), foreignState)
		delta := mergeCRDTDelta(ourState, foreignState)
		if len(delta) > 0 {
			data, err := encodeMergeCommand(delta)
			if err != nil {
				healLogger.Errorf("[%s] encode merge command failed: %v", h.cluster.ID().ShortString(), err)
				return
			}
			if err := h.applyMerge(data); err != nil {
				healLogger.Errorf("[%s] merge from orphan %s failed: %v",
					h.cluster.ID().ShortString(), orphan.ShortString(), err)
				return
			}
			healLogger.Infof("[%s] merged %d keys from orphan %s",
				h.cluster.ID().ShortString(), len(delta), orphan.ShortString())
		}
	}

	healLogger.Infof("[%s] sending healAck to orphan %s — it will wipe and rejoin via requestVoterJoin",
		h.cluster.ID().ShortString(), orphan.ShortString())
	h.member.raftClient.HealAck(orphan)
}

func (h *healer) executeMerge(ctx context.Context, loserLeader peer.ID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.cluster.IsLeader() {
		return
	}

	// ONLY THE MEMBERS OF THE GROUP MERGED ARE TOLD TO YIELD: a peer of a third
	// group, whose state nobody took, would wipe the only copy of it.
	loser, err := h.member.raftClient.ClusterInfo(loserLeader)
	if err != nil {
		healLogger.Errorf("clusterInfo from %s failed: %v", loserLeader.ShortString(), err)
		return
	}

	foreignState, _, err := h.member.raftClient.ExportFSM(loserLeader)
	if err != nil {
		healLogger.Errorf("export FSM from %s failed: %v", loserLeader.ShortString(), err)
		return
	}

	ourState, err := h.member.fsm.ExportState()
	if err != nil {
		healLogger.Errorf("export local FSM failed: %v", err)
		return
	}

	warnForeignWallClockDrift(h.cluster.ID(), foreignState)

	delta := mergeCRDTDelta(ourState, foreignState)

	if len(delta) > 0 {
		data, err := encodeMergeCommand(delta)
		if err != nil {
			healLogger.Errorf("encode merge command failed: %v", err)
			return
		}
		if err := h.applyMerge(data); err != nil {
			healLogger.Errorf("apply merge failed: %v", err)
			return
		}
		healLogger.Infof("[%s] merged %d keys from foreign cluster",
			h.cluster.ID().ShortString(), len(delta))
	}

	h.healAckMembersOf(loser.Members)
}

// applyMerge applies a merge and reports the state machine's refusal as well
// as raft's, since Apply carries the first in its response and not its error.
func (h *healer) applyMerge(data []byte) error {
	resp, err := h.cluster.Apply(data, HealingMergeTimeout)
	if err != nil {
		return err
	}
	return resp.Error
}

// healAckMembersOf tells the members of a merged group that are not this group's
// to yield and join it.
func (h *healer) healAckMembersOf(members []peer.ID) {
	ours := map[peer.ID]bool{}
	for _, m := range h.member.raft.Members() {
		ours[m.ID] = true
	}
	sent := 0
	for _, pid := range members {
		if ours[pid] || pid == h.cluster.ID() {
			continue
		}
		h.member.raftClient.HealAck(pid)
		sent++
	}
	healLogger.Infof("[%s] healing complete — sent healAck to %d members of the merged group (they will rejoin via requestVoterJoin)",
		h.cluster.ID().ShortString(), sent)
}

func (h *healer) yieldAndRejoin(ctx context.Context, winnerLeader peer.ID) {
	healLogger.Infof("[%s] yielding to winner %s",
		h.cluster.ID().ShortString(), winnerLeader.ShortString())

	h.cluster.mu.Lock()
	defer h.cluster.mu.Unlock()
	if h.cluster.closed.Load() {
		return
	}

	// Everything initialize builds is taken down before it is built again: the
	// stream service holds the group's protocol, and a raft not waited for can
	// still apply into the state being wiped.
	healLogger.Infof("[%s] taking down raft for rejoin", h.cluster.ID().ShortString())
	m := h.cluster.current()
	m.takeDown()

	healLogger.Infof("[%s] wiping raft state (log, snapshot and applied data)", h.cluster.ID().ShortString())
	h.wipeRaftState(ctx)

	healLogger.Infof("[%s] re-initializing raft after yield", h.cluster.ID().ShortString())
	if err := h.cluster.initialize(); err != nil {
		healLogger.Errorf("[%s] re-initialize failed: %v — trying again", h.cluster.ID().ShortString(), err)
		go h.cluster.reinitialize(reinitializeFirstWait)
	} else {
		healLogger.Infof("[%s] re-initialize succeeded — should rejoin cluster", h.cluster.ID().ShortString())
	}

	// initialize built a new healer, or reinitialize will, so this one's run loop
	// exits.
	h.superseded.Store(true)
}

const (
	reinitializeFirstWait = 100 * time.Millisecond
	reinitializeMaxWait   = 30 * time.Second
)

// reinitialize builds the member again, backing off, until it is built or the
// cluster closes: a yield that could not build one would leave the group with no
// member and no healer to notice. The lock is let go between tries, so Close is
// never kept waiting on them.
func (c *cluster) reinitialize(wait time.Duration) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(wait):
		}
		c.mu.Lock()
		if c.closed.Load() {
			c.mu.Unlock()
			return
		}
		err := c.initialize()
		c.mu.Unlock()
		if err == nil {
			clusterLogger.Infof("[%s] re-initialize succeeded after a failed one", c.ID().ShortString())
			return
		}
		clusterLogger.Warnf("[%s] re-initialize failed: %v — trying again", c.ID().ShortString(), err)
		wait = min(2*wait, reinitializeMaxWait)
	}
}

// wipeRaftState removes the member's log, snapshot and applied data. The applied
// data must go: rejoining by log replication from the first index would otherwise
// leave entries the winning group never committed in the store, which this voter
// would serve once it led. It finishes whatever cancels ctx, as Close does.
func (h *healer) wipeRaftState(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	storagePrefix := path.Join(RaftStoragePrefix, h.cluster.namespace)
	for _, sub := range []string{"engine", "data"} {
		if err := wipePrefix(ctx, h.cluster.Store(), path.Join(storagePrefix, sub)); err != nil {
			healLogger.Warnf("[%s] failed to wipe %s during a yield: %v", h.cluster.ID().ShortString(), sub, err)
		}
	}
}
