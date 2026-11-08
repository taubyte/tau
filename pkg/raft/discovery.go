package raft

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

var discoveryLogger = logging.Logger("raft-discovery")

// A tracker looks for peers every 100ms while its member finds a group, and
// every settledDiscoveryInterval once a group has it.
const settledDiscoveryInterval = 30 * time.Second

// A tracker keeps every peer it hears of as a candidate, and counts one live only
// while it reaches it itself: a peer another member names may be long gone, and
// a founder nobody can reach would be waited for forever.
type peerTracker struct {
	mu             sync.RWMutex
	startTime      time.Time
	peers          map[peer.ID]time.Duration // peer ID -> time since start when first seen
	heard          map[peer.ID]time.Time
	exchanging     map[peer.ID]bool
	selfID         peer.ID
	lastPeerCount  int
	lastChangeTime time.Time
	probed         map[peer.ID]struct{}

	discoveryInterval atomic.Int64
}

func newPeerTracker(selfID peer.ID) *peerTracker {
	now := time.Now()
	pt := &peerTracker{
		selfID:         selfID,
		startTime:      now,
		peers:          make(map[peer.ID]time.Duration),
		heard:          make(map[peer.ID]time.Time),
		exchanging:     make(map[peer.ID]bool),
		probed:         make(map[peer.ID]struct{}),
		lastPeerCount:  1, // self
		lastChangeTime: now,
	}
	pt.peers[selfID] = 0
	pt.discoveryInterval.Store(100)
	return pt
}

func (pt *peerTracker) setDiscoveryInterval(d time.Duration) {
	pt.discoveryInterval.Store(d.Milliseconds())
}

func (pt *peerTracker) getDiscoveryInterval() time.Duration {
	return time.Duration(pt.discoveryInterval.Load()) * time.Millisecond
}

func (pt *peerTracker) addPeer(pid peer.ID) {
	if pid == pt.selfID {
		return
	}
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if _, exists := pt.peers[pid]; !exists {
		pt.peers[pid] = time.Since(pt.startTime)
		pt.lastPeerCount = len(pt.peers)
		pt.lastChangeTime = time.Now()
		discoveryLogger.Infof("[%s] new peer discovered: %s (total tracked: %d)",
			pt.selfID.ShortString(), pid.ShortString(), pt.lastPeerCount)
	}
}

// heardFrom is pid reached directly: a call it answered, or one it made.
func (pt *peerTracker) heardFrom(pid peer.ID) {
	pt.addPeer(pid)
	pt.mu.Lock()
	pt.heard[pid] = time.Now()
	pt.mu.Unlock()
}

// liveWindow is how long a peer counts live after it was last reached: three
// rounds of exchange, and never under a second.
func (pt *peerTracker) liveWindow() time.Duration {
	return max(3*pt.getDiscoveryInterval(), time.Second)
}

// liveLocked says pid is this member, or was reached within the live window.
func (pt *peerTracker) liveLocked(pid peer.ID, now time.Time, window time.Duration) bool {
	if pid == pt.selfID {
		return true
	}
	at, ok := pt.heard[pid]
	return ok && now.Sub(at) <= window
}

// mergePeers returns the peers new to pt. theirPeers maps a peer id to ms after
// theirStart.
func (pt *peerTracker) mergePeers(theirStart time.Time, theirPeers map[string]int64) []peer.ID {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	var newPeers []peer.ID
	for pidStr, theirMs := range theirPeers {
		pid, err := peer.Decode(pidStr)
		if err != nil || pid == pt.selfID {
			continue
		}

		theirSeenAt := theirStart.Add(time.Duration(theirMs) * time.Millisecond)
		ourEquivalent := theirSeenAt.Sub(pt.startTime)
		if ourEquivalent < 0 {
			ourEquivalent = 0
		}

		existing, exists := pt.peers[pid]
		if !exists {
			pt.peers[pid] = ourEquivalent
			newPeers = append(newPeers, pid)
		} else if ourEquivalent < existing {
			pt.peers[pid] = ourEquivalent
		}
	}

	if len(newPeers) > 0 {
		pt.lastPeerCount = len(pt.peers)
		pt.lastChangeTime = time.Now()
		discoveryLogger.Infof("[%s] peer exchange yielded %d new peers (total tracked: %d)",
			pt.selfID.ShortString(), len(newPeers), pt.lastPeerCount)
	}

	return newPeers
}

func (pt *peerTracker) getPeersMap() (time.Time, map[string]int64) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	now, window := time.Now(), pt.liveWindow()
	m := make(map[string]int64, len(pt.peers))
	for pid, d := range pt.peers {
		if pt.liveLocked(pid, now, window) {
			m[pid.String()] = d.Milliseconds()
		}
	}
	return pt.startTime, m
}

// getFoundingMembers returns the live peers seen before the threshold.
func (pt *peerTracker) getFoundingMembers(threshold time.Duration) []peer.ID {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	now, window := time.Now(), pt.liveWindow()
	var founders []peer.ID
	for pid, seenAt := range pt.peers {
		if seenAt <= threshold && pt.liveLocked(pid, now, window) {
			founders = append(founders, pid)
		}
	}

	sort.Slice(founders, func(i, j int) bool {
		return founders[i].String() < founders[j].String()
	})
	return founders
}

// isLateJoiner returns true if every live peer was discovered after threshold.
func (pt *peerTracker) isLateJoiner(threshold time.Duration) bool {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	now, window := time.Now(), pt.liveWindow()
	live := 0
	for pid, seenAt := range pt.peers {
		if pid == pt.selfID || !pt.liveLocked(pid, now, window) {
			continue
		}
		live++
		if seenAt <= threshold {
			return false
		}
	}
	return live > 0
}

// livePeers returns the peers other than this member that are live.
func (pt *peerTracker) livePeers() []peer.ID {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	now, window := time.Now(), pt.liveWindow()
	var peers []peer.ID
	for pid := range pt.peers {
		if pid != pt.selfID && pt.liveLocked(pid, now, window) {
			peers = append(peers, pid)
		}
	}
	return peers
}

// startExchange claims pid for one exchange, unless one with it is under way.
func (pt *peerTracker) startExchange(pid peer.ID) bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if pt.exchanging[pid] {
		return false
	}
	pt.exchanging[pid] = true
	return true
}

func (pt *peerTracker) endExchange(pid peer.ID) {
	pt.mu.Lock()
	delete(pt.exchanging, pid)
	pt.mu.Unlock()
}

func (pt *peerTracker) allPeers() []peer.ID {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	peers := make([]peer.ID, 0, len(pt.peers))
	for pid := range pt.peers {
		if pid != pt.selfID {
			peers = append(peers, pid)
		}
	}
	return peers
}

// firstProbe reports whether this tracker has not probed pid yet, and counts
// it probed from now on.
func (pt *peerTracker) firstProbe(pid peer.ID) bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if _, done := pt.probed[pid]; done {
		return false
	}
	pt.probed[pid] = struct{}{}
	return true
}

// probe asks pid whether it serves the group by opening a stream on its
// protocol. The peerstore lists a peer that began to serve after connecting
// only once identify's push arrives, which is best effort; a stream that
// negotiates the protocol is recorded in the peerstore from then on.
func (pt *peerTracker) probe(ctx context.Context, h host.Host, pid peer.ID, raftProtocol protocol.ID) bool {
	ctx, cancel := context.WithTimeout(ctx, sendToPeerTimeout)
	defer cancel()
	s, err := h.NewStream(ctx, pid, raftProtocol)
	if err != nil {
		return false
	}
	s.Reset()
	pt.heardFrom(pid)
	return true
}

func supportsRaftProtocol(h host.Host, pid peer.ID, raftProtocol protocol.ID) bool {
	protocols, err := h.Peerstore().GetProtocols(pid)
	if err != nil || len(protocols) == 0 {
		return false
	}
	for _, p := range protocols {
		if p == raftProtocol {
			return true
		}
	}
	return false
}

// runDiscoveryAndExchange discovers peers and exchanges lists with them through
// client until ctx is done. A yield replaces the cluster's client while the
// round under way finishes, so it uses only the one it is handed.
func (pt *peerTracker) runDiscoveryAndExchange(ctx context.Context, c *cluster, client internalClient) {
	host := c.Host()
	raftProtocol := protocol.ID(Protocol(c.namespace))

	discoveryLogger.Infof("[%s] starting peer discovery for namespace %s (interval=%v)",
		pt.selfID.ShortString(), c.namespace, pt.getDiscoveryInterval())

	if d := c.Discovery(); d != nil {
		go pt.runLookups(ctx, c, d, raftProtocol)
	}

	for {
		select {
		case <-ctx.Done():
			discoveryLogger.Infof("[%s] peer discovery stopped (context cancelled)", pt.selfID.ShortString())
			return

		case <-time.After(pt.getDiscoveryInterval()):
			connectedPeers := host.Network().Peers()
			for _, pid := range connectedPeers {
				if pid == c.ID() {
					continue
				}
				if supportsRaftProtocol(host, pid, raftProtocol) {
					pt.addPeer(pid)
				} else if pt.firstProbe(pid) {
					go pt.probe(ctx, host, pid, raftProtocol)
				}
			}

			// EACH PEER ON ITS OWN: one that does not answer holds up its own
			// next exchange, never the others', whose liveness would lapse waiting.
			if client != nil {
				startTime, peersMap := pt.getPeersMap()
				for _, pid := range pt.allPeers() {
					if !pt.startExchange(pid) {
						continue
					}
					go pt.exchange(ctx, c, client, pid, startTime, peersMap)
				}
			}
		}
	}
}

func (pt *peerTracker) exchange(ctx context.Context, c *cluster, client internalClient, pid peer.ID, startTime time.Time, peersMap map[string]int64) {
	defer pt.endExchange(pid)
	theirStart, theirPeers, err := client.ExchangePeers(startTime, peersMap, pid)
	if err != nil {
		discoveryLogger.Infof("[%s] peer exchange with %s failed: %v",
			pt.selfID.ShortString(), pid.ShortString(), err)
		return
	}
	pt.heardFrom(pid)
	for _, newPeer := range pt.mergePeers(theirStart, theirPeers) {
		discoveryLogger.Infof("[%s] dialing newly discovered peer %s",
			pt.selfID.ShortString(), newPeer.ShortString())
		c.dialPeer(ctx, newPeer)
	}
}

// runLookups searches discovery for the group's members until ctx ends, one
// lookup at a time, each left to run until discovery ends it: a DHT query
// takes longer than a tick of the tracker, and one called off with nothing
// found is cached as nothing found until its backoff expires.
func (pt *peerTracker) runLookups(ctx context.Context, c *cluster, d discovery.Discovery, raftProtocol protocol.ID) {
	host := c.Host()
	for {
		peerCh, err := d.FindPeers(ctx, string(raftProtocol))
		if err == nil {
			for p := range peerCh {
				if p.ID != c.ID() && c.connectFound(ctx, p) && supportsRaftProtocol(host, p.ID, raftProtocol) {
					pt.addPeer(p.ID)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pt.getDiscoveryInterval()):
		}
	}
}

// connectFound connects to a peer discovery found, which is usually one this
// node has never been connected to: the peerstore learns a peer's protocols
// from the identify that Connect waits for.
func (c *cluster) connectFound(ctx context.Context, p peer.AddrInfo) bool {
	ctx, cancel := context.WithTimeout(ctx, sendToPeerTimeout)
	defer cancel()
	if err := c.Host().Connect(ctx, p); err != nil {
		discoveryLogger.Infof("[%s] connecting to discovered peer %s failed: %v",
			c.ID().ShortString(), p.ID.ShortString(), err)
		return false
	}
	return true
}

func (c *cluster) dialPeer(ctx context.Context, pid peer.ID) {
	peerInfo := peer.AddrInfo{ID: pid}
	c.Host().Connect(ctx, peerInfo)
}
