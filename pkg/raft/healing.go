package raft

import (
	"context"
	"os"
	"path"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	hraft "github.com/hashicorp/raft"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

var healLogger = logging.Logger("raft-healing")

// healer monitors the cluster for split-brain and orchestrates merge/rejoin.
type healer struct {
	cluster *cluster
	member  *member

	healAckCh     chan peer.ID
	foreignVoteCh chan peer.ID
	healing       atomic.Bool
	mu            sync.Mutex
	superseded    atomic.Bool
	// leaderChanged is when raft last reported this member gaining or losing
	// a leader, in unix nanoseconds: up to then it knew one.
	leaderChanged atomic.Int64
	// dropped is raft's count of the observations it dropped, set before the
	// healer is published; droppedSeen is the count noteDropped last took.
	dropped     func() uint64
	droppedSeen atomic.Uint64
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

// notifyForeignVote is called when we observe a vote/pre-vote from a peer
// not in our raft configuration. Non-blocking: drops if the channel is full.
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
		h.cluster.node.ID().ShortString(), checkInterval, h.cluster.splitBrainDetectionCycles)

	noLeaderCycles := 0
	cycleStart := time.Now()

	for {
		select {
		case <-ctx.Done():
			healLogger.Infof("[%s] healer stopped (context cancelled)", h.cluster.node.ID().ShortString())
			return
		case pid := <-h.foreignVoteCh:
			h.handleForeignVote(ctx, pid)
			continue
		case from := <-h.healAckCh:
			h.healAcked(ctx, from)
			if h.superseded.Load() {
				healLogger.Infof("[%s] healer superseded after yieldAndRejoin — exiting", h.cluster.node.ID().ShortString())
				return
			}
			noLeaderCycles = 0
			continue
		case <-time.After(checkInterval):
		}

		if h.cluster.closed.Load() || h.superseded.Load() {
			healLogger.Infof("[%s] healer stopped (closed=%v superseded=%v)",
				h.cluster.node.ID().ShortString(), h.cluster.closed.Load(), h.superseded.Load())
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
			h.cluster.node.ID().ShortString(), noLeaderCycles, h.cluster.splitBrainDetectionCycles,
			h.member.raft.State())

		if noLeaderCycles < h.cluster.splitBrainDetectionCycles {
			continue
		}

		healLogger.Warnf("[%s] split-brain suspected: %d consecutive leaderless cycles",
			h.cluster.node.ID().ShortString(), noLeaderCycles)

		h.detectAndHeal(ctx)
		if h.superseded.Load() {
			healLogger.Infof("[%s] healer superseded after detectAndHeal — exiting", h.cluster.node.ID().ShortString())
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
			h.cluster.node.ID().ShortString(), from.ShortString())
		return
	}
	healLogger.Infof("[%s] received healAck from %s — yielding immediately",
		h.cluster.node.ID().ShortString(), from.ShortString())
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
	h.noteDropped()
	return h.leaderChanged.Load() >= t.UnixNano()
}

// noteDropped counts the observations raft dropped since the count it last
// took as a change of leader, now: a dropped one may have been one. Raft
// counts a drop after it fails to deliver it, so the watcher can drain the
// channel and read the count before it grows, and hear nothing after; so
// hadLeaderSince takes the count too, and each drop counts once whoever
// takes it first.
func (h *healer) noteDropped() {
	if h.dropped == nil {
		return
	}
	d := h.dropped()
	for {
		seen := h.droppedSeen.Load()
		if d <= seen {
			return
		}
		if h.droppedSeen.CompareAndSwap(seen, d) {
			h.leaderChanged.Store(time.Now().UnixNano())
			return
		}
	}
}

// handleForeignVote reacts to a vote/pre-vote from a non-member peer.
// Ensures the peer is tracked so findForeignPeers can discover it,
// and if we're the leader, absorbs the orphan immediately.
func (h *healer) handleForeignVote(ctx context.Context, pid peer.ID) {
	if h.cluster.closed.Load() {
		return
	}

	h.member.tracker.addPeer(pid)

	if !h.cluster.IsLeader() {
		healLogger.Infof("[%s] tracked foreign voter %s (we are %s, not leader)",
			h.cluster.node.ID().ShortString(), pid.ShortString(), h.member.raft.State())
		return
	}

	healLogger.Infof("[%s] vote request from non-member %s — absorbing as leader",
		h.cluster.node.ID().ShortString(), pid.ShortString())
	h.absorbOrphan(ctx, pid)
}

// registerObserver registers a raft Observer that watches for
// RequestPreVoteRequest and RequestVoteRequest from peers not in the
// current configuration and notifies the healer, and for every change of
// leader.
func (h *healer) registerObserver() {
	ch := make(chan hraft.Observation, 16)
	observer := hraft.NewObserver(ch, false, func(o *hraft.Observation) bool {
		switch o.Data.(type) {
		case hraft.RequestVoteRequest, hraft.RequestPreVoteRequest, hraft.LeaderObservation:
			return true
		default:
			return false
		}
	})
	// The observer is deregistered from the raft it watches, its member's: a
	// yield builds a new member, whose healer registers its own.
	on := h.member.raft
	h.dropped = observer.GetNumDropped
	on.RegisterObserver(observer)

	go func() {
		h.watch(h.cluster.ctx, ch)
		on.DeregisterObserver(observer)
	}()
}

// watch processes what the observer delivers until ctx is done. Raft drops
// an observation the channel has no room for, and one it dropped may have
// been a change of leader: each drop counts as one (see noteDropped), so a
// cycle is never taken for leaderless on an observation the healer did not
// get.
func (h *healer) watch(ctx context.Context, ch <-chan hraft.Observation) {
	for {
		select {
		case <-ctx.Done():
			return
		case obs := <-ch:
			h.processObservation(obs)
			h.noteDropped()
		}
	}
}

func (h *healer) processObservation(obs hraft.Observation) {
	var candidateID string
	switch req := obs.Data.(type) {
	case hraft.LeaderObservation:
		h.leaderChanged.Store(time.Now().UnixNano())
		return
	case hraft.RequestPreVoteRequest:
		candidateID = string(req.ID)
	case hraft.RequestVoteRequest:
		candidateID = string(req.ID)
	default:
		return
	}

	if candidateID == "" {
		return
	}

	pid, err := peer.Decode(candidateID)
	if err != nil {
		return
	}

	if pid == h.cluster.node.ID() {
		return
	}

	members, err := h.member.members()
	if err != nil {
		return
	}
	for _, m := range members {
		if m.ID == pid {
			return
		}
	}

	healLogger.Infof("[%s] observed vote request from non-member %s",
		h.cluster.node.ID().ShortString(), pid.ShortString())
	h.notifyForeignVote(pid)
}

func (h *healer) probeAndMergeAsLeader(ctx context.Context) {
	foreignPeers := h.findForeignPeers()
	if len(foreignPeers) == 0 {
		return
	}

	healLogger.Infof("[%s] probing %d foreign peers as leader", h.cluster.node.ID().ShortString(), len(foreignPeers))

	for _, pid := range foreignPeers {
		info, err := h.member.raftClient.ClusterInfo(pid)
		if err != nil {
			healLogger.Infof("[%s] clusterInfo from foreign %s failed: %v",
				h.cluster.node.ID().ShortString(), pid.ShortString(), err)
			continue
		}

		if info.LeaderID == "" {
			// A peer in no configuration belongs to no group: it is joining
			// one, and its join, not a healAck, is what adds it.
			if info.MemberCount == 0 {
				continue
			}
			healLogger.Infof("[%s] detected orphaned foreign node %s — absorbing",
				h.cluster.node.ID().ShortString(), pid.ShortString())
			h.absorbOrphan(ctx, pid)
			continue
		}

		localInfo := h.localClusterInfo()
		winner := negotiateWinner(localInfo, info)

		healLogger.Infof("[%s] foreign %s: leader=%s members=%d lastIndex=%d → winner=%s",
			h.cluster.node.ID().ShortString(), pid.ShortString(),
			info.LeaderID, info.MemberCount, info.LastIndex, winner)

		if winner == localInfo.NodeID {
			healLogger.Infof("[%s] detected foreign cluster led by %s — we win, initiating merge",
				h.cluster.node.ID().ShortString(), info.LeaderID)

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
		healLogger.Infof("[%s] detectAndHeal skipped: already healing", h.cluster.node.ID().ShortString())
		return
	}
	defer h.healing.Store(false)

	foreignPeers := h.findForeignPeers()
	if len(foreignPeers) == 0 {
		healLogger.Infof("[%s] detectAndHeal: no foreign peers found", h.cluster.node.ID().ShortString())
		return
	}

	healLogger.Infof("[%s] detectAndHeal: found %d foreign peers", h.cluster.node.ID().ShortString(), len(foreignPeers))

	var foreignInfo *ClusterInfoResponse
	var foreignLeader peer.ID
	for _, pid := range foreignPeers {
		info, err := h.member.raftClient.ClusterInfo(pid)
		if err != nil {
			healLogger.Infof("[%s] detectAndHeal: clusterInfo from %s failed: %v",
				h.cluster.node.ID().ShortString(), pid.ShortString(), err)
			continue
		}
		foreignInfo = info
		if info.LeaderID != "" {
			foreignLeader, _ = peer.Decode(info.LeaderID)
		}
		break
	}

	if foreignInfo == nil {
		healLogger.Infof("[%s] detectAndHeal: could not reach any foreign peer", h.cluster.node.ID().ShortString())
		return
	}

	localInfo := h.localClusterInfo()
	winner := negotiateWinner(localInfo, foreignInfo)

	healLogger.Infof("[%s] detectAndHeal: local(leader=%s, members=%d, lastIndex=%d) vs foreign(leader=%s, members=%d, lastIndex=%d) → winner=%s",
		h.cluster.node.ID().ShortString(),
		localInfo.LeaderID, localInfo.MemberCount, localInfo.LastIndex,
		foreignInfo.LeaderID, foreignInfo.MemberCount, foreignInfo.LastIndex,
		winner)

	if foreignLeader == "" {
		// Both clusters are leaderless (config mismatch prevents elections).
		// Everyone wipes and re-initialises; handleBootstrap's founding-member
		// logic deterministically picks the leader (lowest peer ID).
		healLogger.Warnf("[%s] both clusters leaderless — wiping to re-bootstrap",
			h.cluster.node.ID().ShortString())
		h.yieldAndRejoin(ctx, peer.ID(""))
		return
	}

	if winner == localInfo.NodeID {
		return
	}

	healLogger.Infof("[%s] we lose to %s — yielding",
		h.cluster.node.ID().ShortString(), foreignInfo.LeaderID)

	h.yieldAndRejoin(ctx, foreignLeader)
}

// findForeignPeers returns the tracked peers that serve our protocol but are
// not raft members. One the peerstore does not list is probed: what identify
// recorded can be stale, and a member a group removed would never be absorbed.
func (h *healer) findForeignPeers() []peer.ID {
	host := h.cluster.node.Peer()
	raftProtocol := protocol.ID(Protocol(h.cluster.namespace))

	members, err := h.member.members()
	if err != nil {
		healLogger.Infof("[%s] findForeignPeers: cannot get members: %v",
			h.cluster.node.ID().ShortString(), err)
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
			h.cluster.node.ID().ShortString(), len(foreign), len(allTracked), len(members))
	}

	return foreign
}

func (h *healer) localClusterInfo() *ClusterInfoResponse {
	info := &ClusterInfoResponse{
		NodeID: h.cluster.node.ID().String(),
	}
	if leader, err := leaderKnownTo(h.member.raft); err == nil {
		info.LeaderID = leader.String()
	}
	stats := h.member.raft.Stats()
	info.Term = parseUint64(stats["term"])
	info.LastIndex = parseUint64(stats["last_log_index"])
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

// absorbOrphan merges FSM state from a leaderless foreign node, then sends it
// a healAck so it wipes its stale raft state and re-initialises. The orphan's
// handleBootstrap will call requestVoterJoin, which triggers AddVoter on us
// once the orphan has clean state — avoiding the term-conflict that would
// destabilise our cluster if we called AddVoter while the orphan still runs
// a conflicting raft instance.
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
			h.cluster.node.ID().ShortString(), orphan.ShortString(), err)
		return
	}
	if len(foreignState) > 0 {
		ourState, err := h.member.fsm.ExportState()
		if err != nil {
			healLogger.Errorf("[%s] export local FSM failed: %v", h.cluster.node.ID().ShortString(), err)
			return
		}
		warnForeignWallClockDrift(h.cluster.node.ID(), foreignState)
		delta := mergeCRDTDelta(ourState, foreignState)
		if len(delta) > 0 {
			data, err := encodeMergeCommand(delta)
			if err != nil {
				healLogger.Errorf("[%s] encode merge command failed: %v", h.cluster.node.ID().ShortString(), err)
				return
			}
			if err := h.applyMerge(data); err != nil {
				healLogger.Errorf("[%s] merge from orphan %s failed: %v",
					h.cluster.node.ID().ShortString(), orphan.ShortString(), err)
				return
			}
			healLogger.Infof("[%s] merged %d keys from orphan %s",
				h.cluster.node.ID().ShortString(), len(delta), orphan.ShortString())
		}
	}

	healLogger.Infof("[%s] sending healAck to orphan %s — it will wipe and rejoin via requestVoterJoin",
		h.cluster.node.ID().ShortString(), orphan.ShortString())
	h.member.raftClient.HealAck(orphan)
}

func (h *healer) executeMerge(ctx context.Context, loserLeader peer.ID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.cluster.IsLeader() {
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

	warnForeignWallClockDrift(h.cluster.node.ID(), foreignState)

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
			h.cluster.node.ID().ShortString(), len(delta))
	}

	h.addVotersAndHealAck()
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

func (h *healer) addVotersAndHealAck() {
	foreignMembers := h.collectForeignMembers()
	for _, pid := range foreignMembers {
		h.member.raftClient.HealAck(pid)
	}
	healLogger.Infof("[%s] healing complete — sent healAck to %d foreign nodes (they will rejoin via requestVoterJoin)",
		h.cluster.node.ID().ShortString(), len(foreignMembers))
}

func (h *healer) collectForeignMembers() []peer.ID {
	foreignPeers := h.findForeignPeers()
	sort.Slice(foreignPeers, func(i, j int) bool {
		return foreignPeers[i].String() < foreignPeers[j].String()
	})
	return foreignPeers
}

func (h *healer) yieldAndRejoin(ctx context.Context, winnerLeader peer.ID) {
	healLogger.Infof("[%s] yielding to winner %s",
		h.cluster.node.ID().ShortString(), winnerLeader.ShortString())

	h.cluster.mu.Lock()
	defer h.cluster.mu.Unlock()
	if h.cluster.closed.Load() {
		return
	}

	// EVERYTHING INITIALIZE BUILDS IS TAKEN DOWN BEFORE IT IS BUILT AGAIN. The
	// stream service holds the group's protocol, so a second one cannot listen
	// beside it, and a raft that is not waited for can still apply into the
	// state being wiped.
	healLogger.Infof("[%s] taking down raft for rejoin", h.cluster.node.ID().ShortString())
	m := h.cluster.current()
	m.takeDown()

	healLogger.Infof("[%s] wiping raft state (logs, stable and applied data)", h.cluster.node.ID().ShortString())
	h.wipeRaftState(ctx)

	healLogger.Infof("[%s] wiping snapshots", h.cluster.node.ID().ShortString())
	m.snaps.wipeAll()

	healLogger.Infof("[%s] re-initializing raft after yield", h.cluster.node.ID().ShortString())
	if err := h.cluster.initialize(); err != nil {
		healLogger.Errorf("[%s] re-initialize failed: %v", h.cluster.node.ID().ShortString(), err)
	} else {
		healLogger.Infof("[%s] re-initialize succeeded — should rejoin cluster", h.cluster.node.ID().ShortString())
	}

	// initialize() created a new healer; mark ourselves as superseded
	// so the run() loop exits and the old goroutine doesn't interfere
	// with the new healer.
	h.superseded.Store(true)
}

// wipeRaftState clears everything the yielding node believed about the cluster
// it is leaving: its log, its stable state, and the state machine those two
// produced.
//
// THE APPLIED DATA GOES TOO, and leaving it was the hole. A node that yields
// rejoins as a voter and takes the winner's state from the leader — by snapshot
// when the leader has one, and Restore already clears this prefix before
// loading it, or by log replication from the first index when it does not. On
// that second path the entries this node applied in the cluster it is leaving
// are still in the store, under keys the winning group never committed. It is a
// voter; the next election it wins serves them, behind a barrier, as committed
// state.
// wipeRaftState removes the member's logs, stable state and applied data.
// It finishes whatever cancels ctx, as Close does: a raft built over a store
// it half wiped finds logs it cannot read, and panics.
func (h *healer) wipeRaftState(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	store := h.cluster.node.Store()
	storagePrefix := path.Join(RaftStoragePrefix, h.cluster.namespace)

	for _, sub := range []string{"log", "stable", "data"} {
		prefix := path.Join(storagePrefix, sub)
		results, err := store.Query(ctx, query.Query{
			Prefix:   prefix,
			KeysOnly: true,
		})
		if err != nil {
			healLogger.Warnf("[%s] failed to query %s keys during wipe: %v",
				h.cluster.node.ID().ShortString(), sub, err)
			continue
		}
		for result := range results.Next() {
			if result.Error != nil {
				healLogger.Warnf("[%s] error iterating %s keys during wipe: %v",
					h.cluster.node.ID().ShortString(), sub, result.Error)
				break
			}
			if err := store.Delete(ctx, ds.NewKey(result.Key)); err != nil {
				healLogger.Warnf("[%s] failed to delete key %s during wipe: %v",
					h.cluster.node.ID().ShortString(), result.Key, err)
			}
		}
		results.Close()
	}
}

func (f *fileSnapshotStore) wipeAll() {
	if err := os.RemoveAll(f.dir); err != nil {
		healLogger.Warnf("failed to remove snapshot dir %s: %v", f.dir, err)
	}
	if err := os.MkdirAll(f.dir, 0755); err != nil {
		healLogger.Errorf("failed to recreate snapshot dir %s: %v", f.dir, err)
	}
}
