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

	healAckCh     chan struct{}
	foreignVoteCh chan peer.ID
	healing       atomic.Bool
	mu            sync.Mutex
}

func newHealer(c *cluster) *healer {
	return &healer{
		cluster:       c,
		healAckCh:     make(chan struct{}, 1),
		foreignVoteCh: make(chan peer.ID, 16),
	}
}

func (h *healer) signalHealAck() {
	select {
	case h.healAckCh <- struct{}{}:
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
	if checkInterval < HealingProbeInterval {
		checkInterval = HealingProbeInterval
	}

	healLogger.Infof("[%s] healer started (check_interval=%v, detection_cycles=%d)",
		h.cluster.node.ID().ShortString(), checkInterval, SplitBrainDetectionCycles)

	noLeaderCycles := 0

	for {
		select {
		case <-ctx.Done():
			healLogger.Debugf("[%s] healer stopped (context cancelled)", h.cluster.node.ID().ShortString())
			return
		case pid := <-h.foreignVoteCh:
			h.handleForeignVote(ctx, pid)
			continue
		case <-time.After(checkInterval):
		}

		if h.cluster.closed.Load() {
			healLogger.Debugf("[%s] healer stopped (cluster closed)", h.cluster.node.ID().ShortString())
			return
		}

		if h.cluster.IsLeader() {
			noLeaderCycles = 0
			h.probeAndMergeAsLeader(ctx)
			continue
		}

		_, leaderErr := h.cluster.Leader()
		if leaderErr == nil {
			noLeaderCycles = 0
			continue
		}

		noLeaderCycles++
		healLogger.Debugf("[%s] no leader detected (cycle %d/%d, state=%s)",
			h.cluster.node.ID().ShortString(), noLeaderCycles, SplitBrainDetectionCycles,
			h.cluster.raft.State())

		if noLeaderCycles < SplitBrainDetectionCycles {
			continue
		}

		healLogger.Warnf("[%s] split-brain suspected: %d consecutive leaderless cycles",
			h.cluster.node.ID().ShortString(), noLeaderCycles)

		h.detectAndHeal(ctx)
		noLeaderCycles = 0
	}
}

// handleForeignVote reacts to a vote/pre-vote from a non-member peer.
// Ensures the peer is tracked so findForeignPeers can discover it,
// and if we're the leader, absorbs the orphan immediately.
func (h *healer) handleForeignVote(ctx context.Context, pid peer.ID) {
	if h.cluster.closed.Load() {
		return
	}

	h.cluster.tracker.addPeer(pid)

	if !h.cluster.IsLeader() {
		healLogger.Debugf("[%s] tracked foreign voter %s (we are %s, not leader)",
			h.cluster.node.ID().ShortString(), pid.ShortString(), h.cluster.raft.State())
		return
	}

	healLogger.Infof("[%s] vote request from non-member %s — absorbing as leader",
		h.cluster.node.ID().ShortString(), pid.ShortString())
	h.absorbOrphan(ctx, pid)
}

// registerVoteObserver registers a raft Observer that watches for
// RequestPreVoteRequest and RequestVoteRequest from peers not in the
// current configuration and notifies the healer.
func (h *healer) registerVoteObserver() {
	ch := make(chan hraft.Observation, 16)
	observer := hraft.NewObserver(ch, false, func(o *hraft.Observation) bool {
		switch o.Data.(type) {
		case hraft.RequestVoteRequest, hraft.RequestPreVoteRequest:
			return true
		default:
			return false
		}
	})
	h.cluster.raft.RegisterObserver(observer)

	go func() {
		for {
			select {
			case <-h.cluster.ctx.Done():
				h.cluster.raft.DeregisterObserver(observer)
				return
			case obs := <-ch:
				h.processVoteObservation(obs)
			}
		}
	}()
}

func (h *healer) processVoteObservation(obs hraft.Observation) {
	var candidateID string
	switch req := obs.Data.(type) {
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

	members, err := h.cluster.Members()
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
		info, err := h.cluster.raftClient.ClusterInfo(pid)
		if err != nil {
			healLogger.Debugf("[%s] clusterInfo from foreign %s failed: %v",
				h.cluster.node.ID().ShortString(), pid.ShortString(), err)
			continue
		}

		if info.LeaderID == "" {
			healLogger.Infof("[%s] detected orphaned foreign node %s — absorbing",
				h.cluster.node.ID().ShortString(), pid.ShortString())
			h.absorbOrphan(ctx, pid)
			continue
		}

		localInfo := h.localClusterInfo()
		winner := negotiateWinner(localInfo, info)

		healLogger.Debugf("[%s] foreign %s: leader=%s members=%d lastIndex=%d → winner=%s",
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
		healLogger.Debugf("[%s] detectAndHeal skipped: already healing", h.cluster.node.ID().ShortString())
		return
	}
	defer h.healing.Store(false)

	foreignPeers := h.findForeignPeers()
	if len(foreignPeers) == 0 {
		healLogger.Debugf("[%s] detectAndHeal: no foreign peers found", h.cluster.node.ID().ShortString())
		return
	}

	healLogger.Infof("[%s] detectAndHeal: found %d foreign peers", h.cluster.node.ID().ShortString(), len(foreignPeers))

	var foreignInfo *ClusterInfoResponse
	var foreignLeader peer.ID
	for _, pid := range foreignPeers {
		info, err := h.cluster.raftClient.ClusterInfo(pid)
		if err != nil {
			healLogger.Debugf("[%s] detectAndHeal: clusterInfo from %s failed: %v",
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
		healLogger.Debugf("[%s] detectAndHeal: could not reach any foreign peer", h.cluster.node.ID().ShortString())
		return
	}

	if foreignLeader == "" {
		healLogger.Debugf("[%s] detectAndHeal: foreign cluster also leaderless — deferring",
			h.cluster.node.ID().ShortString())
		return
	}

	localInfo := h.localClusterInfo()
	winner := negotiateWinner(localInfo, foreignInfo)

	healLogger.Infof("[%s] detectAndHeal: local(members=%d, lastIndex=%d) vs foreign(leader=%s, members=%d, lastIndex=%d) → winner=%s",
		h.cluster.node.ID().ShortString(),
		localInfo.MemberCount, localInfo.LastIndex,
		foreignInfo.LeaderID, foreignInfo.MemberCount, foreignInfo.LastIndex,
		winner)

	if winner == localInfo.NodeID {
		return
	}

	healLogger.Infof("[%s] we lose to %s — waiting for healAck",
		h.cluster.node.ID().ShortString(), foreignInfo.LeaderID)

	h.yieldAndRejoin(ctx, foreignLeader)
}

// findForeignPeers returns libp2p peers that advertise our protocol but are not raft members.
func (h *healer) findForeignPeers() []peer.ID {
	host := h.cluster.node.Peer()
	raftProtocol := protocol.ID(Protocol(h.cluster.namespace))

	members, err := h.cluster.Members()
	if err != nil {
		healLogger.Debugf("[%s] findForeignPeers: cannot get members: %v",
			h.cluster.node.ID().ShortString(), err)
		return nil
	}
	memberSet := make(map[peer.ID]struct{}, len(members))
	for _, m := range members {
		memberSet[m.ID] = struct{}{}
	}

	allTracked := h.cluster.tracker.allPeers()

	var foreign []peer.ID
	for _, pid := range allTracked {
		if _, inConfig := memberSet[pid]; inConfig {
			continue
		}
		if supportsRaftProtocol(host, pid, raftProtocol) {
			foreign = append(foreign, pid)
		}
	}

	if len(foreign) > 0 {
		healLogger.Debugf("[%s] findForeignPeers: %d foreign out of %d tracked (%d members in config)",
			h.cluster.node.ID().ShortString(), len(foreign), len(allTracked), len(members))
	}

	return foreign
}

func (h *healer) localClusterInfo() *ClusterInfoResponse {
	info := &ClusterInfoResponse{
		NodeID: h.cluster.node.ID().String(),
	}
	if leader, err := h.cluster.Leader(); err == nil {
		info.LeaderID = leader.String()
	}
	if h.cluster.raft != nil {
		stats := h.cluster.raft.Stats()
		info.Term = parseUint64(stats["term"])
		info.LastIndex = parseUint64(stats["last_log_index"])
	}
	if members, err := h.cluster.Members(); err == nil {
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

// absorbOrphan adds a leaderless foreign node as a voter and sends it a healAck
// so it can wipe its stale state and rejoin.
func (h *healer) absorbOrphan(ctx context.Context, orphan peer.ID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.cluster.IsLeader() {
		return
	}

	foreignState, _, err := h.cluster.raftClient.ExportFSM(orphan)
	if err != nil {
		healLogger.Warnf("[%s] export FSM from orphan %s failed (may not be leader there): %v",
			h.cluster.node.ID().ShortString(), orphan.ShortString(), err)
	} else if len(foreignState) > 0 {
		ourState, err := h.cluster.fsm.ExportState()
		if err == nil {
			warnForeignWallClockDrift(h.cluster.node.ID(), foreignState)
			delta := mergeCRDTDelta(ourState, foreignState)
			if len(delta) > 0 {
				data, err := encodeMergeCommand(delta)
				if err == nil {
					if _, err := h.cluster.Apply(data, HealingMergeTimeout); err == nil {
						healLogger.Infof("[%s] merged %d keys from orphan %s",
							h.cluster.node.ID().ShortString(), len(delta), orphan.ShortString())
					}
				}
			}
		}
	}

	h.cluster.raftClient.HealAck(orphan)

	if err := h.cluster.AddVoter(orphan, 10*time.Second); err != nil {
		healLogger.Warnf("[%s] add voter %s failed: %v",
			h.cluster.node.ID().ShortString(), orphan.ShortString(), err)
	} else {
		healLogger.Infof("[%s] absorbed orphan %s",
			h.cluster.node.ID().ShortString(), orphan.ShortString())
	}
}

func (h *healer) executeMerge(ctx context.Context, loserLeader peer.ID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.cluster.IsLeader() {
		return
	}

	foreignState, _, err := h.cluster.raftClient.ExportFSM(loserLeader)
	if err != nil {
		healLogger.Errorf("export FSM from %s failed: %v", loserLeader.ShortString(), err)
		return
	}

	ourState, err := h.cluster.fsm.ExportState()
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
		if _, err := h.cluster.Apply(data, HealingMergeTimeout); err != nil {
			healLogger.Errorf("apply merge failed: %v", err)
			return
		}
		healLogger.Infof("[%s] merged %d keys from foreign cluster",
			h.cluster.node.ID().ShortString(), len(delta))
	}

	h.addVotersAndHealAck()
}

func (h *healer) addVotersAndHealAck() {
	foreignMembers := h.collectForeignMembers()
	for _, pid := range foreignMembers {
		if err := h.cluster.AddVoter(pid, 10*time.Second); err != nil {
			healLogger.Warnf("add voter %s failed: %v", pid.ShortString(), err)
		}
	}
	for _, pid := range foreignMembers {
		h.cluster.raftClient.HealAck(pid)
	}
	healLogger.Infof("[%s] healing complete — notified %d foreign nodes",
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

	select {
	case <-h.healAckCh:
		healLogger.Infof("[%s] received healAck", h.cluster.node.ID().ShortString())
	case <-time.After(HealingMergeTimeout):
		healLogger.Warnf("[%s] healAck timeout — proceeding anyway", h.cluster.node.ID().ShortString())
	case <-ctx.Done():
		return
	}

	if h.cluster.raft != nil {
		healLogger.Infof("[%s] shutting down raft instance for rejoin", h.cluster.node.ID().ShortString())
		h.cluster.raft.Shutdown()
	}

	healLogger.Infof("[%s] wiping raft state (logs + stable)", h.cluster.node.ID().ShortString())
	h.wipeRaftState(ctx)

	if h.cluster.snaps != nil {
		healLogger.Infof("[%s] wiping snapshots", h.cluster.node.ID().ShortString())
		h.cluster.snaps.wipeAll()
	}

	healLogger.Infof("[%s] re-initializing raft after yield", h.cluster.node.ID().ShortString())
	if err := h.cluster.initialize(); err != nil {
		healLogger.Errorf("[%s] re-initialize failed: %v", h.cluster.node.ID().ShortString(), err)
	} else {
		healLogger.Infof("[%s] re-initialize succeeded — should rejoin cluster", h.cluster.node.ID().ShortString())
	}
}

func (h *healer) wipeRaftState(ctx context.Context) {
	store := h.cluster.node.Store()
	storagePrefix := path.Join(RaftStoragePrefix, h.cluster.namespace)

	for _, sub := range []string{"log", "stable"} {
		prefix := path.Join(storagePrefix, sub)
		results, err := store.Query(ctx, query.Query{
			Prefix:   prefix,
			KeysOnly: true,
		})
		if err != nil {
			continue
		}
		for result := range results.Next() {
			if result.Error != nil {
				break
			}
			store.Delete(ctx, ds.NewKey(result.Key))
		}
		results.Close()
	}
}

func (f *fileSnapshotStore) wipeAll() {
	os.RemoveAll(f.dir)
	os.MkdirAll(f.dir, 0755)
}
