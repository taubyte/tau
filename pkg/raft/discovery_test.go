package raft

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"gotest.tools/v3/assert"
)

func TestNewPeerTracker(t *testing.T) {
	selfID, err := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	assert.NilError(t, err)

	pt := newPeerTracker(selfID)

	assert.Assert(t, pt != nil)
	assert.Equal(t, pt.selfID, selfID)
	assert.Assert(t, len(pt.peers) == 1)
	assert.Equal(t, pt.peers[selfID], time.Duration(0))
}

func TestPeerTracker_AddPeer(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peerID, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	pt := newPeerTracker(selfID)

	// Add a new peer
	pt.addPeer(peerID)
	assert.Equal(t, len(pt.peers), 2)

	// Adding self should be ignored
	pt.addPeer(selfID)
	assert.Equal(t, len(pt.peers), 2)

	// Adding same peer again should be ignored
	originalTime := pt.peers[peerID]
	time.Sleep(10 * time.Millisecond)
	pt.addPeer(peerID)
	assert.Equal(t, pt.peers[peerID], originalTime)
}

func TestPeerTracker_MergePeers(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")
	peer2, _ := peer.Decode("QmVvkUhhLaQ4dJEPZB1bGTPNqBpHnXcGLqbNFnZbMSKszN")

	pt := newPeerTracker(selfID)

	theirStart := time.Now().Add(-100 * time.Millisecond)
	theirPeers := map[string]int64{
		peer1.String(): 10, // They saw peer1 10ms after their start
		peer2.String(): 20, // They saw peer2 20ms after their start
	}

	newPeers := pt.mergePeers(theirStart, theirPeers)

	assert.Equal(t, len(newPeers), 2)
	assert.Equal(t, len(pt.peers), 3) // self + 2 new peers

	// Merging self should be ignored
	theirPeers = map[string]int64{
		selfID.String(): 5,
	}
	newPeers = pt.mergePeers(theirStart, theirPeers)
	assert.Equal(t, len(newPeers), 0)
}

func TestPeerTracker_MergePeers_InvalidPeerID(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")

	pt := newPeerTracker(selfID)

	theirStart := time.Now()
	theirPeers := map[string]int64{
		"invalid-peer-id": 10,
	}

	newPeers := pt.mergePeers(theirStart, theirPeers)
	assert.Equal(t, len(newPeers), 0)
	assert.Equal(t, len(pt.peers), 1) // Only self
}

func TestPeerTracker_MergePeers_UpdatesEarlierTime(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	pt := newPeerTracker(selfID)

	// First add with a later time
	time.Sleep(50 * time.Millisecond)
	pt.addPeer(peer1)
	originalTime := pt.peers[peer1]

	// Merge with an earlier time
	theirStart := pt.startTime.Add(-10 * time.Millisecond)
	theirPeers := map[string]int64{
		peer1.String(): 5, // 5ms after their start (which is before our start)
	}

	newPeers := pt.mergePeers(theirStart, theirPeers)
	assert.Equal(t, len(newPeers), 0)                // Not new, just updated
	assert.Assert(t, pt.peers[peer1] < originalTime) // Time should be updated to earlier
}

func TestPeerTracker_GetPeersMap(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	pt := newPeerTracker(selfID)
	pt.addPeer(peer1)

	startTime, peersMap := pt.getPeersMap()

	assert.Equal(t, startTime, pt.startTime)
	assert.Equal(t, len(peersMap), 2)
	assert.Equal(t, peersMap[selfID.String()], int64(0))
	assert.Assert(t, peersMap[peer1.String()] >= 0)
}

func TestPeerTracker_GetFoundingMembers(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")
	peer2, _ := peer.Decode("QmVvkUhhLaQ4dJEPZB1bGTPNqBpHnXcGLqbNFnZbMSKszN")

	pt := newPeerTracker(selfID)

	// Add peer1 immediately (founding member)
	pt.addPeer(peer1)

	// Wait and add peer2 (late joiner)
	time.Sleep(100 * time.Millisecond)
	pt.addPeer(peer2)

	// Threshold of 50ms
	founders := pt.getFoundingMembers(50 * time.Millisecond)

	// Should include self and peer1, but not peer2
	assert.Equal(t, len(founders), 2)
}

func TestPeerTracker_IsLateJoiner(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	t.Run("only_self_is_founder", func(t *testing.T) {
		pt := newPeerTracker(selfID)
		assert.Assert(t, !pt.isLateJoiner(50*time.Millisecond))
	})

	t.Run("early_peer_makes_founder", func(t *testing.T) {
		pt := newPeerTracker(selfID)
		pt.addPeer(peer1) // Added immediately (before threshold)
		assert.Assert(t, !pt.isLateJoiner(50*time.Millisecond))
	})

	t.Run("late_peer_makes_late_joiner", func(t *testing.T) {
		pt := newPeerTracker(selfID)
		time.Sleep(60 * time.Millisecond)
		pt.addPeer(peer1) // Added after threshold
		assert.Assert(t, pt.isLateJoiner(50*time.Millisecond))
	})
}

func TestPeerTracker_AllPeers(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")
	peer2, _ := peer.Decode("QmVvkUhhLaQ4dJEPZB1bGTPNqBpHnXcGLqbNFnZbMSKszN")

	pt := newPeerTracker(selfID)
	pt.addPeer(peer1)
	pt.addPeer(peer2)

	allPeers := pt.allPeers()

	// Should not include self
	assert.Equal(t, len(allPeers), 2)

	// Check that self is not in the list
	for _, p := range allPeers {
		assert.Assert(t, p != selfID)
	}
}

func TestPeerTracker_MergePeers_NegativeTime(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	pt := newPeerTracker(selfID)

	// Their start time is before ours, and they saw peer1 early
	// This should result in ourEquivalent being 0 (clamped)
	theirStart := pt.startTime.Add(-100 * time.Millisecond)
	theirPeers := map[string]int64{
		peer1.String(): 10, // 10ms after their start = 90ms before our start
	}

	newPeers := pt.mergePeers(theirStart, theirPeers)
	assert.Equal(t, len(newPeers), 1)
	assert.Equal(t, pt.peers[peer1], time.Duration(0)) // Clamped to 0
}
