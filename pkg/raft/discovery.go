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

// peerTracker tracks discovered peers and their discovery times
type peerTracker struct {
	mu             sync.RWMutex
	startTime      time.Time
	peers          map[peer.ID]time.Duration // peer ID -> time since start when first seen
	selfID         peer.ID
	lastPeerCount  int       // last known peer count for stability detection
	lastChangeTime time.Time // when peer count last changed
	probed         map[peer.ID]struct{}

	discoveryInterval atomic.Int64
}

func newPeerTracker(selfID peer.ID) *peerTracker {
	now := time.Now()
	pt := &peerTracker{
		selfID:         selfID,
		startTime:      now,
		peers:          make(map[peer.ID]time.Duration),
		probed:         make(map[peer.ID]struct{}),
		lastPeerCount:  1, // self
		lastChangeTime: now,
	}
	pt.peers[selfID] = 0
	pt.discoveryInterval.Store(100)
	return pt
}

// setDiscoveryInterval sets the discovery interval (how often to run discovery)
func (pt *peerTracker) setDiscoveryInterval(d time.Duration) {
	pt.discoveryInterval.Store(d.Milliseconds())
}

// getDiscoveryInterval returns the current discovery interval
func (pt *peerTracker) getDiscoveryInterval() time.Duration {
	return time.Duration(pt.discoveryInterval.Load()) * time.Millisecond
}

// addPeer adds a discovered peer if not already known
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

// mergePeers merges peer info from another node and returns newly discovered peer IDs
// theirStart is when the other node started, theirPeers maps peer ID string to ms since their start
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

// getPeersMap returns our peer map for exchange
func (pt *peerTracker) getPeersMap() (time.Time, map[string]int64) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	m := make(map[string]int64, len(pt.peers))
	for pid, d := range pt.peers {
		m[pid.String()] = d.Milliseconds()
	}
	return pt.startTime, m
}

// getFoundingMembers returns peers seen before the threshold
func (pt *peerTracker) getFoundingMembers(threshold time.Duration) []peer.ID {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	var founders []peer.ID
	for pid, seenAt := range pt.peers {
		if seenAt <= threshold {
			founders = append(founders, pid)
		}
	}

	sort.Slice(founders, func(i, j int) bool {
		return founders[i].String() < founders[j].String()
	})
	return founders
}

// isLateJoiner returns true if we discovered all peers after threshold
func (pt *peerTracker) isLateJoiner(threshold time.Duration) bool {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	if len(pt.peers) <= 1 {
		return false
	}

	for pid, seenAt := range pt.peers {
		if pid == pt.selfID {
			continue
		}
		if seenAt <= threshold {
			return false
		}
	}
	return true
}

// allPeers returns all known peer IDs
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

// probe asks a peer whether it serves the group by opening a stream on the
// group's protocol, and reports whether it does. The peerstore learns what a
// peer serves from the identify run on connecting, so a peer that began to
// serve after the two connected is listed only once identify's push arrives:
// one best-effort attempt, which an identify answer built before the push can
// still overwrite. A stream that negotiates the protocol is recorded in the
// peerstore (basichost's NewStream), which lists the peer from then on.
func (pt *peerTracker) probe(ctx context.Context, h host.Host, pid peer.ID, raftProtocol protocol.ID) bool {
	ctx, cancel := context.WithTimeout(ctx, sendToPeerTimeout)
	defer cancel()
	s, err := h.NewStream(ctx, pid, raftProtocol)
	if err != nil {
		return false
	}
	s.Reset()
	pt.addPeer(pid)
	return true
}

// supportsRaftProtocol checks if a peer explicitly supports the given raft protocol
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
	host := c.node.Peer()
	raftProtocol := protocol.ID(Protocol(c.namespace))

	discoveryLogger.Infof("[%s] starting peer discovery for namespace %s (interval=%v)",
		pt.selfID.ShortString(), c.namespace, pt.getDiscoveryInterval())

	if d := c.node.Discovery(); d != nil {
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
				if pid == c.node.ID() {
					continue
				}
				if supportsRaftProtocol(host, pid, raftProtocol) {
					pt.addPeer(pid)
				} else if pt.firstProbe(pid) {
					go pt.probe(ctx, host, pid, raftProtocol)
				}
			}

			if client != nil {
				for _, pid := range pt.allPeers() {
					startTime, peersMap := pt.getPeersMap()
					theirStart, theirPeers, err := client.ExchangePeers(startTime, peersMap, pid)
					if err != nil {
						discoveryLogger.Infof("[%s] peer exchange with %s failed: %v",
							pt.selfID.ShortString(), pid.ShortString(), err)
						continue
					}
					newPeers := pt.mergePeers(theirStart, theirPeers)
					for _, newPeer := range newPeers {
						discoveryLogger.Infof("[%s] dialing newly discovered peer %s",
							pt.selfID.ShortString(), newPeer.ShortString())
						c.dialPeer(ctx, newPeer)
					}
				}
			}
		}
	}
}

// runLookups searches discovery for the group's members until ctx ends, one
// lookup at a time, each left to run until discovery ends it: a DHT query
// takes longer than a tick of the tracker, and one called off with nothing
// found is cached as nothing found until its backoff expires.
func (pt *peerTracker) runLookups(ctx context.Context, c *cluster, d discovery.Discovery, raftProtocol protocol.ID) {
	host := c.node.Peer()
	for {
		peerCh, err := d.FindPeers(ctx, string(raftProtocol))
		if err == nil {
			for p := range peerCh {
				if p.ID != c.node.ID() && c.connectFound(ctx, p) && supportsRaftProtocol(host, p.ID, raftProtocol) {
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
	if err := c.node.Peer().Connect(ctx, p); err != nil {
		discoveryLogger.Infof("[%s] connecting to discovered peer %s failed: %v",
			c.node.ID().ShortString(), p.ID.ShortString(), err)
		return false
	}
	return true
}

func (c *cluster) dialPeer(ctx context.Context, pid peer.ID) {
	peerInfo := peer.AddrInfo{ID: pid}
	c.node.Peer().Connect(ctx, peerInfo)
}
