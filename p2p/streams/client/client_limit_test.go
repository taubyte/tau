package client

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	keypair "github.com/taubyte/tau/p2p/keypair"
	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/p2p/streams/command/router"
	peerService "github.com/taubyte/tau/p2p/streams/service"
)

const limitPath = "/limit/1.0"

func setLimits(t *testing.T, max int, delay time.Duration) {
	oldMax, oldDelay := MaxInFlightPerPeer, LimitRetryDelay
	MaxInFlightPerPeer, LimitRetryDelay = max, delay
	t.Cleanup(func() { MaxInFlightPerPeer, LimitRetryDelay = oldMax, oldDelay })
}

func limitNode(t *testing.T) peer.Node {
	p, err := peer.New(t.Context(), nil, keypair.NewRaw(), nil, []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, false)
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })
	return p
}

type limitServer struct {
	peer.Node
	router *router.Router
}

// limitPair is a client connected to a real server over TCP, so a reset code
// travels the real wire and error chain.
func limitPair(t *testing.T, handler router.CommandHandler) (*Client, *limitServer) {
	server := limitNode(t)
	svr, err := peerService.New(server, "limit", limitPath)
	require.NoError(t, err)
	t.Cleanup(svr.Stop)
	require.NoError(t, svr.Define("work", handler))
	svr.Start()

	node := limitNode(t)
	require.NoError(t, node.Peer().Connect(t.Context(), peercore.AddrInfo{ID: server.ID(), Addrs: server.Peer().Addrs()}))

	c, err := New(node, limitPath)
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c, &limitServer{Node: server, router: svr.Router()}
}

// resetFirst makes the server reset the first n streams with code, then serve.
func (s *limitServer) resetFirst(n int64, code network.StreamErrorCode) *atomic.Int64 {
	var seen atomic.Int64
	s.Peer().SetStreamHandler(protocol.ID(limitPath), func(strm network.Stream) {
		if seen.Add(1) <= n {
			strm.ResetWithError(code)
			return
		}
		s.router.Handle(strm)
	})
	return &seen
}

func okHandler(context.Context, streams.Connection, command.Body) (cr.Response, error) {
	return cr.Response{"ok": true}, nil
}

func requireSlotsFree(t *testing.T, c *Client, pid peercore.ID) {
	t.Helper()
	v, ok := c.inFlight.Load(pid)
	require.True(t, ok)
	require.Eventually(t, func() bool { return len(v.(chan struct{})) == 0 }, 2*time.Second, 5*time.Millisecond, "a slot was not released")
}

func TestLimit_ConcurrentCallsStayUnderCap(t *testing.T) {
	setLimits(t, 4, time.Millisecond)
	var now, peak atomic.Int64
	c, server := limitPair(t, func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		n := now.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(50 * time.Millisecond)
		now.Add(-1)
		return cr.Response{}, nil
	})

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_, err := c.Send("work", command.Body{}, server.ID())
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	assert.LessOrEqual(t, peak.Load(), int64(4))
	assert.GreaterOrEqual(t, peak.Load(), int64(2))
	requireSlotsFree(t, c, server.ID())
}

func TestLimit_WaiterRespectsDeadline(t *testing.T) {
	setLimits(t, 1, time.Millisecond)
	started, unblock := make(chan struct{}), make(chan struct{})
	var once sync.Once
	c, server := limitPair(t, func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		once.Do(func() { close(started) })
		<-unblock
		return cr.Response{}, nil
	})

	holder := make(chan error, 1)
	go func() {
		_, err := c.Send("work", command.Body{}, server.ID())
		holder <- err
	}()
	<-started

	start := time.Now()
	_, err := c.New("work", To(server.ID()), Timeout(200*time.Millisecond)).Do()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "waiting for a free stream")
	assert.Less(t, time.Since(start), 2*time.Second)

	// A peer found by discovery is limited too, and the failure must be safe to Close.
	resCh, err := c.New("work", Timeout(time.Second)).Do()
	require.NoError(t, err)
	res := <-resCh
	require.NotNil(t, res)
	require.ErrorContains(t, res.Error(), "waiting for a free stream")
	res.Close()

	close(unblock)
	require.NoError(t, <-holder)
	_, err = c.Send("work", command.Body{}, server.ID())
	require.NoError(t, err)
	requireSlotsFree(t, c, server.ID())
}

func TestLimit_RefusedCallIsRetried(t *testing.T) {
	setLimits(t, 16, 10*time.Millisecond)
	c, server := limitPair(t, okHandler)
	seen := server.resetFirst(2, network.StreamResourceLimitExceeded)

	resp, err := c.Send("work", command.Body{}, server.ID())
	require.NoError(t, err)
	assert.Equal(t, true, resp["ok"])
	assert.Equal(t, int64(3), seen.Load())
	requireSlotsFree(t, c, server.ID())
}

func TestLimit_RetriesAreCapped(t *testing.T) {
	setLimits(t, 16, 10*time.Millisecond)
	c, server := limitPair(t, okHandler)
	seen := server.resetFirst(100, network.StreamResourceLimitExceeded)

	_, err := c.Send("work", command.Body{}, server.ID())
	var se *network.StreamError
	require.True(t, errors.As(err, &se), "got %v", err)
	assert.Equal(t, network.StreamResourceLimitExceeded, se.ErrorCode)
	assert.Equal(t, int64(1+limitRetries), seen.Load())
	requireSlotsFree(t, c, server.ID())
}

func TestLimit_OtherResetIsNotRetried(t *testing.T) {
	setLimits(t, 16, 10*time.Millisecond)
	c, server := limitPair(t, okHandler)
	seen := server.resetFirst(100, network.StreamRateLimited)

	_, err := c.Send("work", command.Body{}, server.ID())
	require.Error(t, err)
	assert.Equal(t, int64(1), seen.Load())
	requireSlotsFree(t, c, server.ID())
}

func TestLimit_RetryStaysInsideDeadline(t *testing.T) {
	setLimits(t, 16, 20*time.Second)
	c, server := limitPair(t, okHandler)
	seen := server.resetFirst(100, network.StreamResourceLimitExceeded)

	start := time.Now()
	resCh, err := c.New("work", To(server.ID()), Timeout(300*time.Millisecond)).Do()
	require.NoError(t, err)
	res := <-resCh
	require.Error(t, res.Error())
	res.Close()
	assert.Less(t, time.Since(start), 3*time.Second)
	assert.Equal(t, int64(1), seen.Load())
	requireSlotsFree(t, c, server.ID())
}
