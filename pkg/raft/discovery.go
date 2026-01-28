package raft

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// peerTracker tracks discovered peers and their discovery times
type peerTracker struct {
	mu             sync.RWMutex
	startTime      time.Time
	peers          map[peer.ID]time.Duration // peer ID -> time since start when first seen
	selfID         peer.ID
	lastPeerCount  int       // last known peer count for stability detection
	lastChangeTime time.Time // when peer count last changed

	// discoveryInterval stores the interval in milliseconds (atomic for thread-safe updates)
	discoveryInterval atomic.Int64
}

func newPeerTracker(selfID peer.ID) *peerTracker {
	now := time.Now()
	pt := &peerTracker{
		selfID:         selfID,
		startTime:      now,
		peers:          make(map[peer.ID]time.Duration),
		lastPeerCount:  1, // self
		lastChangeTime: now,
	}
	pt.peers[selfID] = 0
	// Start with fast discovery interval (100ms) during bootstrap
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

		// Convert their observation time to our timeline
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

	// Update stability tracking if new peers were added
	if len(newPeers) > 0 {
		pt.lastPeerCount = len(pt.peers)
		pt.lastChangeTime = time.Now()
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

// runDiscoveryAndExchange discovers peers and exchanges lists until ctx is done
// Uses dynamic interval that can be adjusted via SetDiscoveryInterval()
func (pt *peerTracker) runDiscoveryAndExchange(ctx context.Context, c *cluster) {
	discovery := c.node.Discovery()
	host := c.node.Peer()
	raftProtocol := protocol.ID(Protocol(c.namespace))

	for {
		select {
		case <-ctx.Done():
			return

		case <-time.After(pt.getDiscoveryInterval()):
			// Add connected peers that explicitly support our raft protocol
			for _, pid := range host.Network().Peers() {
				if pid != c.node.ID() && supportsRaftProtocol(host, pid, raftProtocol) {
					pt.addPeer(pid)
				}
			}

			// Peers from discovery.FindPeers are already filtered by namespace
			if discovery != nil {
				discoverCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				peerCh, err := discovery.FindPeers(discoverCtx, c.namespace)
				if err == nil {
					for p := range peerCh {
						// Also verify protocol support for discovered peers
						if supportsRaftProtocol(host, p.ID, raftProtocol) {
							pt.addPeer(p.ID)
						}
					}
				}
				cancel()
			}

			// Exchange peers with known peers - this will naturally fail
			// for peers that don't support our protocol
			if c.raftClient != nil {
				for _, pid := range pt.allPeers() {
					startTime, peersMap := pt.getPeersMap()
					theirStart, theirPeers, err := c.raftClient.ExchangePeers(startTime, peersMap, pid)
					if err == nil {
						newPeers := pt.mergePeers(theirStart, theirPeers)
						for _, newPeer := range newPeers {
							c.dialPeer(ctx, newPeer)
						}
					}
				}
			}
		}
	}
}

func (c *cluster) dialPeer(ctx context.Context, pid peer.ID) {
	peerInfo := peer.AddrInfo{ID: pid}
	c.node.Peer().Connect(ctx, peerInfo)
}
