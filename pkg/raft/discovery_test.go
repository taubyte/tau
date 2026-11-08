package raft

import (
	"context"
	"slices"
	"strings"
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

	pt.addPeer(peerID)
	assert.Equal(t, len(pt.peers), 2)

	pt.addPeer(selfID)
	assert.Equal(t, len(pt.peers), 2)

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
		peer1.String(): 10,
		peer2.String(): 20,
	}

	newPeers := pt.mergePeers(theirStart, theirPeers)

	assert.Equal(t, len(newPeers), 2)
	assert.Equal(t, len(pt.peers), 3)

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
	assert.Equal(t, len(pt.peers), 1)
}

func TestPeerTracker_MergePeers_UpdatesEarlierTime(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	pt := newPeerTracker(selfID)

	time.Sleep(50 * time.Millisecond)
	pt.addPeer(peer1)
	originalTime := pt.peers[peer1]

	theirStart := pt.startTime.Add(-10 * time.Millisecond)
	theirPeers := map[string]int64{
		peer1.String(): 5, // 5ms after their start (which is before our start)
	}

	newPeers := pt.mergePeers(theirStart, theirPeers)
	assert.Equal(t, len(newPeers), 0)
	assert.Assert(t, pt.peers[peer1] < originalTime)
}

func TestPeerTracker_GetPeersMap(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	pt := newPeerTracker(selfID)
	pt.addPeer(peer1)

	startTime, peersMap := pt.getPeersMap()
	assert.Equal(t, startTime, pt.startTime)
	assert.Equal(t, len(peersMap), 1, "a peer the tracker never reached is vouched for")
	assert.Equal(t, peersMap[selfID.String()], int64(0))

	pt.heardFrom(peer1)
	_, peersMap = pt.getPeersMap()
	assert.Equal(t, len(peersMap), 2)
	assert.Assert(t, peersMap[peer1.String()] >= 0)
}

// A PEER LAST REACHED LONGER AGO THAN THE LIVE WINDOW is no founder and nobody
// hears of it from this tracker: a dead founder leaves every tracker.
func TestPeerTracker_APeerNotReachedLatelyIsNotLive(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	pt := newPeerTracker(selfID)
	pt.heardFrom(peer1)
	assert.Equal(t, len(pt.getFoundingMembers(time.Minute)), 2)

	pt.mu.Lock()
	pt.heard[peer1] = time.Now().Add(-2 * pt.liveWindow())
	pt.mu.Unlock()
	assert.DeepEqual(t, pt.getFoundingMembers(time.Minute), []peer.ID{selfID})
	_, peersMap := pt.getPeersMap()
	assert.Equal(t, len(peersMap), 1)
	assert.Equal(t, len(pt.livePeers()), 0)
	assert.Assert(t, !pt.isLateJoiner(0))
	assert.DeepEqual(t, pt.allPeers(), []peer.ID{peer1})
}

func TestPeerTracker_GetFoundingMembers(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")
	peer2, _ := peer.Decode("QmVvkUhhLaQ4dJEPZB1bGTPNqBpHnXcGLqbNFnZbMSKszN")

	pt := newPeerTracker(selfID)

	pt.addPeer(peer1)
	assert.Equal(t, len(pt.getFoundingMembers(50*time.Millisecond)), 1, "a peer the tracker never reached is a founder")
	pt.heardFrom(peer1)

	time.Sleep(100 * time.Millisecond)
	pt.heardFrom(peer2)

	founders := pt.getFoundingMembers(50 * time.Millisecond)

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
		pt.heardFrom(peer1)
		assert.Assert(t, !pt.isLateJoiner(50*time.Millisecond))
	})

	t.Run("late_peer_makes_late_joiner", func(t *testing.T) {
		pt := newPeerTracker(selfID)
		time.Sleep(60 * time.Millisecond)
		pt.heardFrom(peer1)
		assert.Assert(t, pt.isLateJoiner(50*time.Millisecond))
	})

	t.Run("a_late_peer_never_reached_makes_no_late_joiner", func(t *testing.T) {
		pt := newPeerTracker(selfID)
		time.Sleep(60 * time.Millisecond)
		pt.addPeer(peer1)
		assert.Assert(t, !pt.isLateJoiner(50*time.Millisecond))
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

	assert.Equal(t, len(allPeers), 2)

	for _, p := range allPeers {
		assert.Assert(t, p != selfID)
	}
}

func TestPeerTracker_MergePeers_NegativeTime(t *testing.T) {
	selfID, _ := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
	peer1, _ := peer.Decode("QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt")

	pt := newPeerTracker(selfID)

	// Seen before our start, so clamped to 0.
	theirStart := pt.startTime.Add(-100 * time.Millisecond)
	theirPeers := map[string]int64{
		peer1.String(): 10, // 10ms after their start = 90ms before our start
	}

	newPeers := pt.mergePeers(theirStart, theirPeers)
	assert.Equal(t, len(newPeers), 1)
	assert.Equal(t, pt.peers[peer1], time.Duration(0))
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

type discoveryNode struct {
	taupeer.Node
	discovery discovery.Discovery
}

func (n discoveryNode) Discovery() discovery.Discovery { return n.discovery }

func TestDiscovery_AGroupOnlyDiscoveryFindsIsJoined(t *testing.T) {
	const namespace = "found-by-discovery"
	meeting := newRendezvous()
	nodeA, nodeB := newMockNode(t), newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	b, err := New(discoveryNode{Node: nodeB, discovery: meeting.pointOf(nodeB.Host())}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	waitLeader(t, b, 5*time.Second)
	require.Eventually(t, func() bool { return meeting.holds(Protocol(namespace), nodeB.ID()) },
		5*time.Second, 10*time.Millisecond, "b never advertised the group")
	unreachable := strangerID(t)
	meeting.register(Protocol(namespace), peer.AddrInfo{ID: unreachable})
	require.NotContains(t, nodeA.Host().Network().Peers(), nodeB.ID())

	a, err := New(discoveryNode{Node: nodeA, discovery: meeting.pointOf(nodeA.Host())}, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })

	require.Eventually(t, func() bool {
		return slices.Contains(a.(*cluster).current().tracker.allPeers(), nodeB.ID())
	}, 5*time.Second, 10*time.Millisecond, "a never tracked the member discovery found")
	require.Eventually(t, func() bool {
		members, err := b.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 20*time.Millisecond, "a never joined the group discovery found")
	require.NotContains(t, a.(*cluster).current().tracker.allPeers(), unreachable)
}

// slowPoint answers a search only after delay, the way a DHT query takes its
// time, and answers nothing to a search called off sooner.
type slowPoint struct {
	discovery.Discovery
	delay time.Duration
}

func (p slowPoint) FindPeers(ctx context.Context, name string, opts ...discovery.Option) (<-chan peer.AddrInfo, error) {
	found := make(chan peer.AddrInfo)
	go func() {
		defer close(found)
		select {
		case <-ctx.Done():
			return
		case <-time.After(p.delay):
		}
		answer, err := p.Discovery.FindPeers(ctx, name, opts...)
		if err != nil {
			return
		}
		for info := range answer {
			select {
			case found <- info:
			case <-ctx.Done():
				return
			}
		}
	}()
	return found, nil
}

func TestDiscovery_ASearchSlowerThanATickFindsTheGroup(t *testing.T) {
	const namespace = "found-by-a-slow-search"
	meeting := newRendezvous()
	nodeA, nodeB := newMockNode(t), newMockNode(t)
	require.NoError(t, taupeer.LinkAllPeers())

	b, err := New(discoveryNode{Node: nodeB, discovery: meeting.pointOf(nodeB.Host())}, namespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })
	waitLeader(t, b, 5*time.Second)
	require.Eventually(t, func() bool { return meeting.holds(Protocol(namespace), nodeB.ID()) },
		5*time.Second, 10*time.Millisecond, "b never advertised the group")
	require.NotContains(t, nodeA.Host().Network().Peers(), nodeB.ID())

	slow := slowPoint{Discovery: meeting.pointOf(nodeA.Host()), delay: 300 * time.Millisecond}
	a, err := New(discoveryNode{Node: nodeA, discovery: slow}, namespace,
		WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second))
	require.NoError(t, err)
	t.Cleanup(func() { a.Close() })

	require.Eventually(t, func() bool {
		members, err := b.Members()
		return err == nil && len(members) == 2
	}, 10*time.Second, 20*time.Millisecond, "a never joined the group a slow search found")
}

// pushless is a mock node that refuses identify's push, so all it knows a
// peer serves is what the identify run on connecting told it.
func pushless(t *testing.T) taupeer.Node {
	node := newMockNode(t)
	node.Host().RemoveStreamHandler(identify.IDPush)
	return node
}

func connectAllOf(t testing.TB, nodes ...taupeer.Node) {
	t.Helper()
	require.NoError(t, taupeer.LinkAllPeers())
	for i, a := range nodes {
		for _, b := range nodes[i+1:] {
			require.NoError(t, a.Host().Connect(t.Context(), peer.AddrInfo{ID: b.ID(), Addrs: b.Host().Addrs()}))
		}
	}
}

func TestDiscovery_APeerConnectedBeforeItServedTheGroupIsFound(t *testing.T) {
	t.Run("a node joins a group its peer started after they connected", func(t *testing.T) {
		const namespace = "connected-first-join"
		leaderNode, joinerNode := pushless(t), pushless(t)
		connectAllOf(t, leaderNode, joinerNode)

		leader, err := New(leaderNode, namespace, WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
		require.NoError(t, err)
		t.Cleanup(func() { leader.Close() })
		waitLeader(t, leader, 5*time.Second)
		const timeout = 10 * time.Second
		began := time.Now()
		joiner, err := New(joinerNode, namespace,
			WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(timeout))
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
			go func() {
				cl, err := New(node, namespace,
					WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(2*time.Second))
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

func TestDiscovery_AMemberTheGroupLetsInLooksForPeersAsAFounderDoes(t *testing.T) {
	const namespace = "let-in-settles"
	nodes := []taupeer.Node{newMockNode(t), newMockNode(t)}
	slices.SortFunc(nodes, func(a, b taupeer.Node) int { return strings.Compare(a.ID().String(), b.ID().String()) })
	connectAllOf(t, nodes...)

	// The lowest founder bootstraps the group once its own bootstrap ends,
	// after the other's has.
	bootstrap := []time.Duration{2 * time.Second, time.Second}
	opened := make(chan *cluster, len(nodes))
	failed := make(chan error, len(nodes))
	for i, node := range nodes {
		go func() {
			cl, err := New(node, namespace,
				WithTimeouts(testTimeoutConfig()), WithBootstrapTimeout(bootstrap[i]))
			if err != nil {
				failed <- err
				return
			}
			opened <- cl.(*cluster)
		}()
	}
	var members []*cluster
	for range nodes {
		select {
		case cl := <-opened:
			t.Cleanup(func() { cl.Close() })
			members = append(members, cl)
		case err := <-failed:
			t.Error(err)
		}
	}
	require.Len(t, members, len(nodes))
	require.Eventually(t, func() bool {
		for _, m := range members {
			if config, err := m.Members(); err != nil || len(config) != len(nodes) {
				return false
			}
		}
		return true
	}, 10*time.Second, 20*time.Millisecond, "the founders did not form one group")

	for _, m := range members {
		require.Eventually(t, func() bool { return m.current().tracker.getDiscoveryInterval() == settledDiscoveryInterval },
			5*time.Second, 20*time.Millisecond, "a member of the group still looks for peers at its bootstrap's pace")
	}
}
