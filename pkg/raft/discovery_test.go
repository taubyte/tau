package raft

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/identify"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
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

// rendezvous is a discovery the nodes of a test share: what one advertises
// under a name, every one searching that name finds. libp2p's own mock expires
// a registration made with no TTL at once, and Advertise is called with none.
type rendezvous struct {
	mu    sync.Mutex
	names map[string]map[peer.ID]peer.AddrInfo
}

func newRendezvous() *rendezvous {
	return &rendezvous{names: make(map[string]map[peer.ID]peer.AddrInfo)}
}

func (r *rendezvous) register(name string, info peer.AddrInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[name] == nil {
		r.names[name] = make(map[peer.ID]peer.AddrInfo)
	}
	r.names[name][info.ID] = info
}

func (r *rendezvous) holds(name string, id peer.ID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.names[name][id]
	return ok
}

// pointOf is h's way into the rendezvous.
func (r *rendezvous) pointOf(h host.Host) discovery.Discovery {
	return rendezvousPoint{rendezvous: r, host: h}
}

type rendezvousPoint struct {
	*rendezvous
	host host.Host
}

func (p rendezvousPoint) Advertise(_ context.Context, name string, _ ...discovery.Option) (time.Duration, error) {
	p.register(name, peer.AddrInfo{ID: p.host.ID(), Addrs: p.host.Addrs()})
	return time.Hour, nil
}

func (p rendezvousPoint) FindPeers(_ context.Context, name string, _ ...discovery.Option) (<-chan peer.AddrInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	found := make(chan peer.AddrInfo, len(p.names[name]))
	for _, info := range p.names[name] {
		found <- info
	}
	close(found)
	return found, nil
}

// discoveryNode is a node whose discovery is the one a test hands it.
type discoveryNode struct {
	taupeer.Node
	discovery discovery.Discovery
}

func (n discoveryNode) Discovery() discovery.Discovery { return n.discovery }

// TestDiscovery_AGroupOnlyDiscoveryFindsIsJoined: a group advertises
// Protocol(namespace) and the tracker searched for the bare namespace, so
// discovery never yielded a member; and the peer it yields is one this node is
// not connected to, whose protocols the peerstore does not know until it is. A
// node that can reach a group only through discovery now tracks it and joins
// it, and passes over an advertised peer it cannot reach.
func TestDiscovery_AGroupOnlyDiscoveryFindsIsJoined(t *testing.T) {
	const namespace = "found-by-discovery"
	meeting := newRendezvous()
	nodeA, nodeB := newMockNode(t), newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	b, err := New(discoveryNode{Node: nodeB, discovery: meeting.pointOf(nodeB.Peer())}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	waitLeader(t, b, 5*time.Second)
	require.Eventually(t, func() bool { return meeting.holds(Protocol(namespace), nodeB.ID()) },
		5*time.Second, 10*time.Millisecond, "b never advertised the group")
	unreachable := strangerID(t)
	meeting.register(Protocol(namespace), peer.AddrInfo{ID: unreachable})
	require.NotContains(t, nodeA.Peer().Network().Peers(), nodeB.ID())

	a, err := New(discoveryNode{Node: nodeA, discovery: meeting.pointOf(nodeA.Peer())}, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second), WithSnapshotRoot(t.TempDir()))
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })

	require.Eventually(t, func() bool {
		return slices.Contains(a.(*cluster).tracker.allPeers(), nodeB.ID())
	}, 5*time.Second, 10*time.Millisecond, "a never tracked the member discovery found")
	require.Eventually(t, func() bool {
		members, err := b.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 20*time.Millisecond, "a never joined the group discovery found")
	require.NotContains(t, a.(*cluster).tracker.allPeers(), unreachable)
}

// pushless is a mock node that refuses identify's push, so all it knows a
// peer serves is what the identify run on connecting told it.
func pushless(t *testing.T) taupeer.Node {
	node := newMockNode(t)
	node.Peer().RemoveStreamHandler(identify.IDPush)
	return node
}

// connectAllOf connects every pair of nodes.
func connectAllOf(t *testing.T, nodes ...taupeer.Node) {
	t.Helper()
	require.NoError(t, taupeer.LinkAllPeers())
	for i, a := range nodes {
		for _, b := range nodes[i+1:] {
			require.NoError(t, a.Peer().Connect(t.Context(), peer.AddrInfo{ID: b.ID(), Addrs: b.Peer().Addrs()}))
		}
	}
}

// TestDiscovery_APeerConnectedBeforeItServedTheGroupIsFound: a fleet connects
// its nodes first and starts groups after, and the peerstore, the only place
// the tracker and the bootstrap looked, learns that a connected peer serves a
// group only from identify's push. With the push lost, a node never found the
// group its peer started after they connected, and founded one of its own;
// nodes that started a group together after connecting each founded one. A
// connected peer the peerstore does not list is now probed, once per start,
// and the bootstrap asks over the protocol the probe settles, the one a join
// is asked over, so the joiner joins without waiting out its timeout.
func TestDiscovery_APeerConnectedBeforeItServedTheGroupIsFound(t *testing.T) {
	t.Run("a node joins a group its peer started after they connected", func(t *testing.T) {
		const namespace = "connected-first-join"
		leaderNode, joinerNode := pushless(t), pushless(t)
		connectAllOf(t, leaderNode, joinerNode)

		leader, err := New(leaderNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
		require.NoError(t, err)
		t.Cleanup(func() { leader.Close() })
		waitLeader(t, leader, 5*time.Second)
		const timeout = 10 * time.Second
		began := time.Now()
		joiner, err := New(joinerNode, namespace,
			WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(timeout), WithSnapshotRoot(t.TempDir()))
		require.NoError(t, err)
		t.Cleanup(func() { joiner.Close() })
		require.Less(t, time.Since(began), timeout/2, "the joiner waited out its bootstrap timeout instead of joining")

		require.Eventually(t, func() bool {
			members, err := leader.Members()
			return err == nil && len(members) == 2
		}, 10*time.Second, 20*time.Millisecond, "the joiner never joined the group its peer started")
		leaderOf(t, leader, joiner)
	})

	t.Run("nodes that start a group after connecting form one", func(t *testing.T) {
		const namespace = "connected-first-found"
		nodes := []taupeer.Node{pushless(t), pushless(t), pushless(t)}
		connectAllOf(t, nodes...)

		opened := make(chan Cluster, len(nodes))
		failed := make(chan error, len(nodes))
		for _, node := range nodes {
			root := t.TempDir()
			go func() {
				cl, err := New(node, namespace,
					WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second), WithSnapshotRoot(root))
				if err != nil {
					failed <- err
					return
				}
				opened <- cl
			}()
		}
		var clusters []Cluster
		for range nodes {
			select {
			case cl := <-opened:
				t.Cleanup(func() { cl.Close() })
				clusters = append(clusters, cl)
			case err := <-failed:
				t.Error(err)
			}
		}
		require.Len(t, clusters, len(nodes))
		leader := leaderOf(t, clusters...)
		require.Eventually(t, func() bool {
			members, err := leader.Members()
			return err == nil && len(members) == len(nodes)
		}, 10*time.Second, 20*time.Millisecond, "the nodes did not form one group")
	})
}
