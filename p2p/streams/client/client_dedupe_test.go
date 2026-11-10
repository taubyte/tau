package client

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/discovery"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	peerService "github.com/taubyte/tau/p2p/streams/service"
)

// listDiscovery serves whatever list holds, the way a DHT round re-finds peers
// the client already knows.
type listDiscovery struct {
	discovery.Discovery
	list atomic.Pointer[[]peercore.AddrInfo]
}

func (d *listDiscovery) FindPeers(context.Context, string, ...discovery.Option) (<-chan peercore.AddrInfo, error) {
	ch := make(chan peercore.AddrInfo, 16)
	for _, p := range *d.list.Load() {
		ch <- p
	}
	close(ch)
	return ch, nil
}

func (d *listDiscovery) set(servers ...*dedupeServer) {
	list := make([]peercore.AddrInfo, 0, len(servers))
	for _, s := range servers {
		list = append(list, s.info)
	}
	d.list.Store(&list)
}

type discoveryNode struct {
	peer.Node
	d discovery.Discovery
}

func (n discoveryNode) Discovery() discovery.Discovery { return n.d }

type dedupeServer struct {
	info  peercore.AddrInfo
	calls atomic.Int32
}

func dedupeServe(t *testing.T) *dedupeServer {
	node := limitNode(t)
	s := &dedupeServer{info: peercore.AddrInfo{ID: node.ID(), Addrs: node.Peer().Addrs()}}
	svr, err := peerService.New(node, "limit", limitPath)
	require.NoError(t, err)
	t.Cleanup(svr.Stop)
	require.NoError(t, svr.Define("work", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		s.calls.Add(1)
		return cr.Response{"ok": true}, nil
	}))
	svr.Start()
	return s
}

func dedupeClient(t *testing.T, retry time.Duration, servers ...*dedupeServer) (*Client, *listDiscovery, peer.Node) {
	old := PeerRetryInterval
	PeerRetryInterval = retry
	t.Cleanup(func() { PeerRetryInterval = old })

	node := limitNode(t)
	d := &listDiscovery{}
	d.set(servers...)
	c, err := New(discoveryNode{Node: node, d: d}, limitPath)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c, d, node
}

func collect(t *testing.T, resCh <-chan *Response) int {
	n := 0
	for r := range resCh {
		assert.NoError(t, r.Error())
		r.Close()
		n++
	}
	return n
}

func TestSend_NoPeerContactedTwice(t *testing.T) {
	a, b := dedupeServe(t), dedupeServe(t)
	c, _, _ := dedupeClient(t, 20*time.Millisecond, a, b)
	time.Sleep(100 * time.Millisecond)

	const calls = 20
	for range calls {
		resCh, err := c.New("work", Threshold(3), Timeout(2*time.Second)).Do()
		require.NoError(t, err)
		assert.Equal(t, 2, collect(t, resCh))
	}
	assert.EqualValues(t, calls, a.calls.Load())
	assert.EqualValues(t, calls, b.calls.Load())

	// a peer named in To is used too, so discovery must not hand it out again
	a.calls.Store(0)
	require.NoError(t, c.node.Peer().Connect(t.Context(), a.info))
	resCh, err := c.New("work", To(a.info.ID, a.info.ID), Threshold(3), Timeout(2*time.Second)).Do()
	require.NoError(t, err)
	assert.Equal(t, 2, collect(t, resCh))
	assert.EqualValues(t, 1, a.calls.Load())
}

func TestSend_ReturnsWhenKnownPeersExhausted(t *testing.T) {
	a, b := dedupeServe(t), dedupeServe(t)
	c, _, _ := dedupeClient(t, 20*time.Millisecond, a, b)
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	resCh, err := c.New("work", Threshold(3), Timeout(3*time.Second)).Do()
	require.NoError(t, err)
	assert.Equal(t, 2, collect(t, resCh))
	assert.Less(t, time.Since(start), time.Second)
}

func TestSend_StartupStillWaitsForDiscovery(t *testing.T) {
	a, b := dedupeServe(t), dedupeServe(t)
	c, d, _ := dedupeClient(t, 400*time.Millisecond, a)

	// asked right after New, with only a known: b must still be waited for
	resCh, err := c.New("work", Threshold(2), Timeout(3*time.Second)).Do()
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond) // the first discovery round has ended by now
	d.set(a, b)
	assert.Equal(t, 2, collect(t, resCh))
}

func TestSend_NoKnownPeerKeepsWaiting(t *testing.T) {
	a := dedupeServe(t)
	c, d, _ := dedupeClient(t, 20*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	resCh, err := c.New("work", Threshold(2), Timeout(3*time.Second)).Do()
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond) // discovery rounds pass with nothing found
	d.set(a)
	assert.Equal(t, 1, collect(t, resCh))
}

func TestSend_ClosesExtraStreams(t *testing.T) {
	a, b := dedupeServe(t), dedupeServe(t)
	c, _, node := dedupeClient(t, time.Second, a, b)
	require.NoError(t, node.Peer().Connect(t.Context(), a.info))
	require.NoError(t, node.Peer().Connect(t.Context(), b.info))

	_, err := c.Send("work", command.Body{}, a.info.ID, b.info.ID)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		open := 0
		for _, conn := range node.Peer().Network().Conns() {
			for _, s := range conn.GetStreams() {
				if string(s.Protocol()) == limitPath {
					open++
				}
			}
		}
		return open == 0
	}, 3*time.Second, 20*time.Millisecond, "streams left open after Send")
}
