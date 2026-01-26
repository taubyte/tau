package raft

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// peerTracker tracks discovered peers and their discovery times
type peerTracker struct {
	mu        sync.RWMutex
	startTime time.Time
	peers     map[peer.ID]time.Duration // peer ID -> time since start when first seen
	selfID    peer.ID
}

func newPeerTracker(selfID peer.ID) *peerTracker {
	pt := &peerTracker{
		selfID:    selfID,
		startTime: time.Now(),
		peers:     make(map[peer.ID]time.Duration),
	}
	// Self is always a founding member
	pt.peers[selfID] = 0
	return pt
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
			ourEquivalent = 0 // They saw it before we started
		}

		existing, exists := pt.peers[pid]
		if !exists {
			pt.peers[pid] = ourEquivalent
			newPeers = append(newPeers, pid)
		} else if ourEquivalent < existing {
			pt.peers[pid] = ourEquivalent
		}
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

	// Sort for deterministic order
	sort.Slice(founders, func(i, j int) bool {
		return founders[i].String() < founders[j].String()
	})
	return founders
}

// isLateJoiner returns true if we discovered all peers after threshold
func (pt *peerTracker) isLateJoiner(threshold time.Duration) bool {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	// If we only know about ourselves, we're a founder
	if len(pt.peers) <= 1 {
		return false
	}

	// Check if we discovered any peers before threshold
	for pid, seenAt := range pt.peers {
		if pid == pt.selfID {
			continue
		}
		if seenAt <= threshold {
			return false // We saw someone early, we're a founder
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

// runDiscoveryAndExchange discovers peers and exchanges lists until ctx is done
func (pt *peerTracker) runDiscoveryAndExchange(ctx context.Context, c *cluster) {
	discovery := c.node.Discovery()
	host := c.node.Peer()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			// First, check already-connected peers from the host's peer store
			// This works even without DHT discovery
			for _, pid := range host.Network().Peers() {
				if pid != c.node.ID() {
					pt.addPeer(pid)
				}
			}

			// Discover new peers via libp2p discovery (DHT, etc.)
			if discovery != nil {
				discoverCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				peerCh, err := discovery.FindPeers(discoverCtx, c.namespace)
				if err == nil {
					for p := range peerCh {
						pt.addPeer(p.ID)
					}
				}
				cancel()
			}

			// Exchange with all known peers using our raft client
			if c.raftClient != nil {
				for _, pid := range pt.allPeers() {
					startTime, peersMap := pt.getPeersMap()
					theirStart, theirPeers, err := c.raftClient.ExchangePeers(startTime, peersMap, pid)
					if err == nil {
						newPeers := pt.mergePeers(theirStart, theirPeers)
						// Dial newly discovered peers so we can exchange with them
						for _, newPeer := range newPeers {
							c.dialPeer(ctx, newPeer)
						}
					}
				}
			}
		}
	}
}

// dialPeer attempts to connect to a peer
func (c *cluster) dialPeer(ctx context.Context, pid peer.ID) {
	// Use the node's host to connect
	peerInfo := peer.AddrInfo{ID: pid}
	_ = c.node.Peer().Connect(ctx, peerInfo)
}
