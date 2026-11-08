package raft

import (
	"bytes"
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ds "github.com/ipfs/go-datastore"
	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

var healLogger = logging.Logger("raft-healing")

type healer struct {
	cluster *cluster
	member  *member

	healAckCh     chan healAck
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
		healAckCh:     make(chan healAck, 1),
		foreignVoteCh: make(chan peer.ID, 16),
	}
}

// healAck is a receipt and the leader that sent it.
type healAck struct {
	from    peer.ID
	receipt receipt
}

func (h *healer) signalHealAck(from peer.ID, r receipt) {
	select {
	case h.healAckCh <- healAck{from: from, receipt: r}:
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

	if h.cluster.hasArchive() {
		go h.replayOnceLeading(ctx)
	}

	noLeaderCycles := 0
	cycleStart := time.Now()
	var leaderlessSince time.Time
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
		case ack := <-h.healAckCh:
			h.healAcked(ctx, ack)
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
			leaderlessSince = time.Time{}
			cycleStart = time.Now()
			h.replayArchive(ctx)
			h.probeAndMergeAsLeader(ctx)
			continue
		}

		began := cycleStart
		hadLeader := h.hadLeaderSince(cycleStart)
		cycleStart = time.Now()
		if hadLeader {
			noLeaderCycles = 0
			leaderlessSince = time.Time{}
			continue
		}

		if leaderlessSince.IsZero() {
			leaderlessSince = began
		}
		if after := h.cluster.quorumLossAfter; after > 0 && time.Since(leaderlessSince) >= after {
			h.recoverFromQuorumLoss(ctx)
			if h.superseded.Load() {
				healLogger.Infof("[%s] healer superseded after recovering from a lost quorum — exiting", h.cluster.ID().ShortString())
				return
			}
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
func (h *healer) healAcked(ctx context.Context, ack healAck) {
	if leader, err := h.cluster.Leader(); err == nil && leader == ack.from {
		healLogger.Infof("[%s] healAck from %s, which leads us — nothing to yield",
			h.cluster.ID().ShortString(), ack.from.ShortString())
		return
	}
	healLogger.Infof("[%s] received healAck from %s — yielding immediately",
		h.cluster.ID().ShortString(), ack.from.ShortString())
	h.yieldAndRejoin(ctx, ack.from, ack.receipt)
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

		leaderPID, err := peer.Decode(info.LeaderID)
		if err != nil {
			continue
		}
		if winner == localInfo.NodeID {
			healLogger.Infof("[%s] detected foreign cluster led by %s — we win, initiating merge",
				h.cluster.ID().ShortString(), info.LeaderID)
			h.executeMerge(ctx, leaderPID)
			continue
		}
		// THE WINNER MAY NOT KNOW THIS GROUP: an exchange with its leader puts this
		// one in its tracker, so its next probe takes us rather than whenever its
		// discovery would.
		startTime, peers := h.member.tracker.getPeersMap()
		h.member.raftClient.ExchangePeers(startTime, peers, leaderPID)
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
			h.yieldAndRejoin(ctx, peer.ID(""), holdingNothing{})
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
	healLogger.Infof("[%s] detectAndHeal: local(leader=%s, members=%d, lastIndex=%d) vs foreign(leader=%s, members=%d, lastIndex=%d)",
		h.cluster.ID().ShortString(),
		localInfo.LeaderID, localInfo.MemberCount, localInfo.LastIndex,
		foreignInfo.LeaderID, foreignInfo.MemberCount, foreignInfo.LastIndex)

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
		h.yieldAndRejoin(ctx, peer.ID(""), holdingNothing{})
		return
	}

	// A LEADERLESS MEMBER IS ALWAYS THE ONE TAKEN: it cannot take anyone's state,
	// and which formation is lower settles only which of two leaders takes the
	// other.
	//
	// A LOSER HOLDING STATE WAITS TO BE TAKEN: the winner's leader exports it,
	// merges it, and only then hands it a receipt to yield on. Yielding first would
	// wipe what the winner never had.
	if !pristine {
		healLogger.Infof("[%s] we lose to %s — waiting for its leader to take our state",
			h.cluster.ID().ShortString(), foreignInfo.LeaderID)
		// An exchange with the winner's leader puts this member in its tracker, so
		// its next probe finds it rather than whenever discovery would.
		startTime, peers := h.member.tracker.getPeersMap()
		h.member.raftClient.ExchangePeers(startTime, peers, foreignLeader)
		return
	}
	healLogger.Infof("[%s] we lose to %s and hold nothing — yielding",
		h.cluster.ID().ShortString(), foreignInfo.LeaderID)
	h.yieldAndRejoin(ctx, foreignLeader, holdingNothing{})
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
	info.Formation = h.member.raft.Formation()
	if members, err := h.member.members(); err == nil {
		info.MemberCount = len(members)
	}
	return info
}

// negotiateWinner picks the same side from either: the lower formation when both
// have one, a value neither side's writes or membership move, so two leaders never
// each take the other.
func negotiateWinner(a, b *ClusterInfoResponse) string {
	if len(a.Formation) > 0 && len(b.Formation) > 0 && !bytes.Equal(a.Formation, b.Formation) {
		if bytes.Compare(a.Formation, b.Formation) < 0 {
			return a.NodeID
		}
		return b.NodeID
	}
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
	exp, err := h.member.raftClient.ExportFSM(orphan, 0)
	if err != nil {
		healLogger.Warnf("[%s] export FSM from orphan %s failed: %v",
			h.cluster.ID().ShortString(), orphan.ShortString(), err)
		return
	}
	foreignState := exp.state
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
	h.member.raftClient.HealAck(orphan, exp.formation, exp.cut)
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

	exp, err := h.member.raftClient.ExportFSM(loserLeader, MergeFreeze)
	if err != nil {
		healLogger.Errorf("export FSM from %s failed: %v", loserLeader.ShortString(), err)
		return
	}
	if !bytes.Equal(exp.formation, loser.Formation) {
		healLogger.Warnf("[%s] %s's formation changed during the merge — trying again later", h.cluster.ID().ShortString(), loserLeader.ShortString())
		return
	}
	foreignState := exp.state

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

	h.healAckMembersOf(loser.Members, exp)
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
func (h *healer) healAckMembersOf(members []peer.ID, exp export) {
	ours := map[peer.ID]bool{}
	for _, m := range h.member.raft.Members() {
		ours[m.ID] = true
	}
	sent := 0
	for _, pid := range members {
		if ours[pid] || pid == h.cluster.ID() {
			continue
		}
		h.member.raftClient.HealAck(pid, exp.formation, exp.cut)
		sent++
	}
	healLogger.Infof("[%s] healing complete — sent healAck to %d members of the merged group (they will rejoin via requestVoterJoin)",
		h.cluster.ID().ShortString(), sent)
}

// archiveKey is where a member keeps the state it carries out of a formation that
// lost its quorum, until the group it forms with it has taken it.
func (c *cluster) archiveKey() ds.Key {
	return ds.NewKey(path.Join(RaftStoragePrefix, c.namespace, "archive"))
}

func (c *cluster) hasArchive() bool {
	has, err := c.Store().Has(c.ctx, c.archiveKey())
	return err == nil && has
}

// memberAnswer is what a member of the formation said of itself, nil when it said
// nothing.
type memberAnswer struct {
	id   peer.ID
	info *ClusterInfoResponse
}

// recoverFromQuorumLoss forms a new group out of a formation fewer than a quorum
// of whose members answer, carrying what this member holds, when it is the lowest
// of the members that answer: the others are taken by its group as the orphans
// they are, receipts and all. Nothing is done while any member knows a leader, or
// while a quorum answers, which can still elect one.
func (h *healer) recoverFromQuorumLoss(ctx context.Context) {
	m := h.member
	if m.raft.Pristine() {
		return
	}
	formation := m.raft.Formation()
	members := m.raft.Members()
	answers := make(chan memberAnswer, len(members))
	asked := 0
	for _, mb := range members {
		if mb.ID == h.cluster.ID() {
			continue
		}
		asked++
		go func() {
			info, err := m.raftClient.ClusterInfo(mb.ID)
			if err != nil {
				info = nil
			}
			answers <- memberAnswer{id: mb.ID, info: info}
		}()
	}
	reachable := []peer.ID{h.cluster.ID()}
	led := false
	for range asked {
		a := <-answers
		switch {
		case a.info == nil:
		case a.info.LeaderID != "":
			led = true
		case bytes.Equal(a.info.Formation, formation):
			reachable = append(reachable, a.id)
		}
	}
	if led || len(reachable) > len(members)/2 {
		return
	}
	slices.SortFunc(reachable, func(a, b peer.ID) int { return strings.Compare(a.String(), b.String()) })
	if reachable[0] != h.cluster.ID() {
		healLogger.Infof("[%s] quorum lost; %s, the lowest member that answers, forms the new group", h.cluster.ID().ShortString(), reachable[0].ShortString())
		return
	}

	cut := m.raft.CommitIndex()
	if err := m.raft.appliedTo(cut, time.After(exportDrainWait)); err != nil {
		healLogger.Warnf("[%s] quorum lost; applying what it committed: %v", h.cluster.ID().ShortString(), err)
		return
	}
	state, err := m.fsm.ExportState()
	if err != nil {
		healLogger.Warnf("[%s] quorum lost; exporting what it holds: %v", h.cluster.ID().ShortString(), err)
		return
	}
	data, err := encodeMergeCommand(state)
	if err != nil {
		healLogger.Warnf("[%s] quorum lost; encoding what it holds: %v", h.cluster.ID().ShortString(), err)
		return
	}
	// THE ARCHIVE IS WRITTEN BEFORE ANYTHING IS WIPED, and a member that holds
	// nothing but an archive forms its group with it: a crash between the two
	// loses nothing.
	if err := h.cluster.Store().Put(ctx, h.cluster.archiveKey(), data); err != nil {
		healLogger.Warnf("[%s] quorum lost; archiving what it holds: %v", h.cluster.ID().ShortString(), err)
		return
	}
	healLogger.Warnf("[%s] %d of %d members answer and none leads — forming a new group carrying %d keys",
		h.cluster.ID().ShortString(), len(reachable), len(members), len(state))
	h.yieldAndRejoin(ctx, "", receipt{formation: formation, cut: cut})
}

// replayArchive merges the state this member carried out of a formation that lost
// its quorum into the group it leads, then lets the archive go.
func (h *healer) replayArchive(ctx context.Context) {
	if !h.cluster.IsLeader() {
		return
	}
	data, err := h.cluster.Store().Get(ctx, h.cluster.archiveKey())
	if errors.Is(err, ds.ErrNotFound) {
		return
	}
	if err != nil {
		healLogger.Warnf("[%s] reading its archive: %v", h.cluster.ID().ShortString(), err)
		return
	}
	if err := h.applyMerge(data); err != nil {
		healLogger.Warnf("[%s] merging its archive: %v", h.cluster.ID().ShortString(), err)
		return
	}
	if err := h.cluster.Store().Delete(ctx, h.cluster.archiveKey()); err != nil {
		healLogger.Warnf("[%s] letting its archive go: %v", h.cluster.ID().ShortString(), err)
		return
	}
	healLogger.Infof("[%s] the state it carried out of a formation that lost its quorum is in its group", h.cluster.ID().ShortString())
}

// replayOnceLeading replays the archive as soon as the member leads, rather than
// at its next check.
func (h *healer) replayOnceLeading(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for h.cluster.hasArchive() && !h.superseded.Load() {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		h.replayArchive(ctx)
	}
}

// yieldGuard is what lets a yield wipe what the member holds. It is asked once the
// member's raft is down, when nothing more can commit: what held when the yield
// was decided may not hold by the time it is made.
type yieldGuard interface {
	allows(m *member) bool
}

// receipt is another group's word that it holds the state of formation up to cut.
type receipt struct {
	formation []byte
	cut       uint64
}

func (r receipt) allows(m *member) bool {
	return len(r.formation) > 0 && bytes.Equal(m.raft.Formation(), r.formation) && m.raft.CommitIndex() <= r.cut
}

// holdingNothing lets a member yield that has nothing to lose.
type holdingNothing struct{}

func (holdingNothing) allows(m *member) bool { return m.raft.Pristine() }

func (h *healer) yieldAndRejoin(ctx context.Context, winnerLeader peer.ID, guard yieldGuard) {
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

	if guard.allows(m) {
		healLogger.Infof("[%s] wiping raft state (log, snapshot and applied data)", h.cluster.ID().ShortString())
		h.wipeRaftState(ctx)
		h.cluster.yielded = true
	} else {
		healLogger.Warnf("[%s] the yield no longer holds once the raft is down — rebuilding without wiping", h.cluster.ID().ShortString())
	}

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
