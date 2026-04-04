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

	healAckCh chan struct{}
	healing   atomic.Bool
	mu        sync.Mutex
}

func newHealer(c *cluster) *healer {
	return &healer{
		cluster:   c,
		healAckCh: make(chan struct{}, 1),
	}
}

func (h *healer) signalHealAck() {
	select {
	case h.healAckCh <- struct{}{}:
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

	failedCycles := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(checkInterval):
		}

		if h.cluster.closed.Load() {
			return
		}

		state := h.cluster.raft.State()

		switch state {
		case hraft.Leader, hraft.Follower:
			failedCycles = 0
			if state == hraft.Leader {
				h.probeAndMergeAsLeader(ctx)
			}
			continue
		case hraft.Candidate:
			failedCycles++
		default:
			continue
		}

		if failedCycles < SplitBrainDetectionCycles {
			continue
		}

		healLogger.Warnf("[%s] split-brain suspected: %d consecutive failed election cycles",
			h.cluster.node.ID().ShortString(), failedCycles)

		h.detectAndHeal(ctx)
		failedCycles = 0
	}
}

func (h *healer) probeAndMergeAsLeader(ctx context.Context) {
	foreignPeers := h.findForeignPeers()
	if len(foreignPeers) == 0 {
		return
	}

	for _, pid := range foreignPeers {
		info, err := h.cluster.raftClient.ClusterInfo(pid)
		if err != nil || info.LeaderID == "" {
			continue
		}

		localInfo := h.localClusterInfo()
		winner := negotiateWinner(localInfo, info)

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
		return
	}
	defer h.healing.Store(false)

	foreignPeers := h.findForeignPeers()
	if len(foreignPeers) == 0 {
		return
	}

	var foreignInfo *ClusterInfoResponse
	var foreignLeader peer.ID
	for _, pid := range foreignPeers {
		info, err := h.cluster.raftClient.ClusterInfo(pid)
		if err != nil || info.LeaderID == "" {
			continue
		}
		foreignInfo = info
		foreignLeader, _ = peer.Decode(info.LeaderID)
		break
	}

	if foreignInfo == nil || foreignLeader == "" {
		return
	}

	localInfo := h.localClusterInfo()
	winner := negotiateWinner(localInfo, foreignInfo)

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
		return nil
	}
	memberSet := make(map[peer.ID]struct{}, len(members))
	for _, m := range members {
		memberSet[m.ID] = struct{}{}
	}

	var foreign []peer.ID
	for _, pid := range h.cluster.tracker.allPeers() {
		if _, inConfig := memberSet[pid]; inConfig {
			continue
		}
		if supportsRaftProtocol(host, pid, raftProtocol) {
			foreign = append(foreign, pid)
		}
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
		h.cluster.raft.Shutdown()
	}

	h.wipeRaftState(ctx)

	if h.cluster.snaps != nil {
		h.cluster.snaps.wipeAll()
	}

	if err := h.cluster.initialize(); err != nil {
		healLogger.Errorf("[%s] re-initialize failed: %v", h.cluster.node.ID().ShortString(), err)
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
