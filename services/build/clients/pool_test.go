package clients

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/rpc"
	authv1 "github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v1/authv1connect"
)

func TestNewPoolRefusesWhatItCannotDialWith(t *testing.T) {
	mn := newMocknet(t)
	h := newHost(t, mn)
	r := discovery.Static()

	for _, tc := range []struct {
		name string
		call func() (*Pool, error)
	}{
		{"no host", func() (*Pool, error) { return NewPool(nil, r, AuthProtocol, 4) }},
		{"no resolver", func() (*Pool, error) { return NewPool(h, nil, AuthProtocol, 4) }},
		{"no protocol", func() (*Pool, error) { return NewPool(h, r, "", 4) }},
		{"zero size", func() (*Pool, error) { return NewPool(h, r, AuthProtocol, 0) }},
		{"negative size", func() (*Pool, error) { return NewPool(h, r, AuthProtocol, -1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := tc.call()
			require.Error(t, err)
			require.Nil(t, p)
		})
	}
}

// The claim the whole design rests on: a peer that does not serve the protocol
// answers with connect.CodeUnavailable, exactly as a peer that is down does and
// exactly as a raft follower refusing a write will. Three cases, one branch —
// which is why there is no leader lookup, no forwarding and no client cache
// keyed by leadership anywhere in build.
func TestAPeerThatDoesNotServeTheProtocolIsUnavailable(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)
	silent := newHost(t, mn) // linked, alive, listening on nothing

	p, err := NewPool(caller, discovery.Static(silent.ID()), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	var seen error
	err = p.Do(t.Context(), func(c *rpc.Client) error {
		repos := authv1connect.NewRepositoryServiceClient(c.HTTP(), c.URL())
		_, seen = repos.Get(t.Context(), connect.NewRequest(&authv1.RepositoryRef{RepositoryKey: "github.com/a/b"}))
		return seen
	})

	require.Error(t, seen)
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(seen),
		"a peer not serving the protocol must be Unavailable, or Pool.Do needs a second branch")
	require.ErrorIs(t, err, ErrNoBackend)
}

func TestDoWalksPastUnavailableAndStopsOnAnAnswer(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)
	unavailable := newHost(t, mn)
	answering := newHost(t, mn)

	p, err := NewPool(caller, discovery.Static(unavailable.ID(), answering.ID()), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	var tried []peer.ID
	err = p.Do(t.Context(), func(c *rpc.Client) error {
		// The URL's authority is the peer id, which is how a caller knows
		// which peer a client reaches without asking the transport.
		tried = append(tried, peer.ID(""))
		if len(tried) == 1 {
			return connect.NewError(connect.CodeUnavailable, errors.New("not the leader"))
		}
		return nil
	})

	require.NoError(t, err)
	require.Len(t, tried, 2, "the first peer's Unavailable must move on to the second")
}

// The other half of the same rule, and the one that keeps a considered refusal
// from being retried N times: any code but Unavailable is the answer.
func TestDoReturnsAnyOtherCodeImmediately(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)

	p, err := NewPool(caller, discovery.Static(newHost(t, mn).ID(), newHost(t, mn).ID()), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	calls := 0
	err = p.Do(t.Context(), func(c *rpc.Client) error {
		calls++
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("job is not terminal"))
	})

	require.Equal(t, 1, calls, "a FailedPrecondition is an answer, not a routing problem")
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.NotErrorIs(t, err, ErrNoBackend)
}

func TestDoWithNoPeersIsErrNoBackend(t *testing.T) {
	mn := newMocknet(t)
	p, err := NewPool(newHost(t, mn), discovery.Static(), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	called := false
	err = p.Do(t.Context(), func(*rpc.Client) error { called = true; return nil })
	require.ErrorIs(t, err, ErrNoBackend)
	require.False(t, called)
}

// failingResolver is a Resolver whose Peers never answers, which is what a live
// discovery.Discovery does when the context ends before a peer is found.
type failingResolver struct{ failed []peer.ID }

func (f *failingResolver) Any(ctx context.Context) (peer.ID, error) { return "", ctx.Err() }
func (f *failingResolver) Peers(context.Context, int) ([]peer.ID, error) {
	return nil, errors.New("nothing found")
}
func (f *failingResolver) Known() []peer.ID  { return nil }
func (f *failingResolver) Failed(id peer.ID) { f.failed = append(f.failed, id) }

func TestDoReportsAResolverFailureAsNoBackend(t *testing.T) {
	mn := newMocknet(t)
	p, err := NewPool(newHost(t, mn), &failingResolver{}, AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	err = p.Do(t.Context(), func(*rpc.Client) error { return nil })
	require.ErrorIs(t, err, ErrNoBackend)
}

// A peer that was unavailable is reported back, so a live Discovery stops
// leading with it, and its client is dropped rather than kept open.
func TestAnUnavailablePeerIsReportedAndDropped(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)
	dead := newHost(t, mn)

	r := &recordingResolver{peers: []peer.ID{dead.ID()}}
	p, err := NewPool(caller, r, AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	err = p.Do(t.Context(), func(*rpc.Client) error {
		return connect.NewError(connect.CodeUnavailable, errors.New("down"))
	})
	require.ErrorIs(t, err, ErrNoBackend)
	require.Equal(t, []peer.ID{dead.ID()}, r.failed)
	require.Equal(t, 0, p.held())
}

type recordingResolver struct {
	peers  []peer.ID
	failed []peer.ID
}

func (r *recordingResolver) Any(context.Context) (peer.ID, error)          { return r.peers[0], nil }
func (r *recordingResolver) Peers(context.Context, int) ([]peer.ID, error) { return r.peers, nil }
func (r *recordingResolver) Known() []peer.ID                              { return r.peers }
func (r *recordingResolver) Failed(id peer.ID)                             { r.failed = append(r.failed, id) }

// held is the number of cached clients. It exists for the eviction test: the
// bound is the whole reason the cache is safe to keep.
func (p *Pool) held() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

func TestThePoolNeverHoldsMoreThanItsBound(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)

	var ids []peer.ID
	for range 5 {
		ids = append(ids, newHost(t, mn).ID())
	}

	p, err := NewPool(caller, discovery.Static(ids...), AuthProtocol, 2)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	// Every peer answers Unavailable, so Do walks all the ones it asked for.
	// It asks for max, so at most max clients are ever built — and the drop on
	// Unavailable means none are left behind either.
	require.ErrorIs(t, p.Do(t.Context(), func(*rpc.Client) error {
		return connect.NewError(connect.CodeUnavailable, errors.New("down"))
	}), ErrNoBackend)
	require.LessOrEqual(t, p.held(), 2)

	// Now with peers that answer, so nothing is dropped and the cache fills.
	calls := 0
	require.NoError(t, p.Do(t.Context(), func(*rpc.Client) error {
		calls++
		return nil
	}))
	require.Equal(t, 1, calls)
	require.LessOrEqual(t, p.held(), 2)
}

func TestAClientIsReusedBetweenCalls(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)
	server := serveAuth(t, mn, func(string) (*authv1.Repository, error) {
		return &authv1.Repository{ProjectId: "p"}, nil
	})

	p, err := NewPool(caller, discovery.Static(server.ID()), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	var first, second *rpc.Client
	require.NoError(t, p.Do(t.Context(), func(c *rpc.Client) error { first = c; return nil }))
	require.NoError(t, p.Do(t.Context(), func(c *rpc.Client) error { second = c; return nil }))
	require.Same(t, first, second)
}

func TestCloseIsIdempotentAndStopsDo(t *testing.T) {
	mn := newMocknet(t)
	p, err := NewPool(newHost(t, mn), discovery.Static(newHost(t, mn).ID()), AuthProtocol, 4)
	require.NoError(t, err)

	require.NoError(t, p.Do(t.Context(), func(*rpc.Client) error { return nil }))
	p.Close()
	p.Close()

	require.ErrorIs(t, p.Do(t.Context(), func(*rpc.Client) error { return nil }), ErrClosed)
	require.Equal(t, 0, p.held())
}

// A pool whose host is closed cannot build a client, which is the one error
// inside the loop that is not a call's answer.
func TestADialThatCannotBeBuiltIsTriedPastRatherThanReturned(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)
	good := serveAuth(t, mn, func(string) (*authv1.Repository, error) {
		return &authv1.Repository{ProjectId: "p"}, nil
	})

	// An empty peer id fails rpc.NewClient's Validate; the next peer answers.
	p, err := NewPool(caller, discovery.Static(peer.ID(""), good.ID()), AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	calls := 0
	require.NoError(t, p.Do(t.Context(), func(*rpc.Client) error { calls++; return nil }))
	require.Equal(t, 1, calls)
}

// Close can land while a Do is in flight — a node shutting down does exactly
// that — so the two paths that re-check it are reachable, and these are the
// interleavings that reach them.
func TestCloseDuringADoIsRefusedRatherThanRaced(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)
	target := newHost(t, mn).ID()

	t.Run("between resolving and dialling", func(t *testing.T) {
		var p *Pool
		r := &hookResolver{peers: []peer.ID{target}, onPeers: func() { p.Close() }}
		var err error
		p, err = NewPool(caller, r, AuthProtocol, 4)
		require.NoError(t, err)

		require.ErrorIs(t, p.Do(t.Context(), func(*rpc.Client) error { return nil }), ErrClosed)
	})

	t.Run("between the call and the drop", func(t *testing.T) {
		p, err := NewPool(caller, discovery.Static(target), AuthProtocol, 4)
		require.NoError(t, err)

		err = p.Do(t.Context(), func(*rpc.Client) error {
			p.Close()
			return connect.NewError(connect.CodeUnavailable, errors.New("down"))
		})
		require.ErrorIs(t, err, ErrNoBackend)
		require.Equal(t, 0, p.held())
	})
}

type hookResolver struct {
	peers   []peer.ID
	onPeers func()
}

func (h *hookResolver) Any(context.Context) (peer.ID, error) { return h.peers[0], nil }
func (h *hookResolver) Peers(context.Context, int) ([]peer.ID, error) {
	if h.onPeers != nil {
		h.onPeers()
	}
	return h.peers, nil
}
func (h *hookResolver) Known() []peer.ID { return h.peers }
func (h *hookResolver) Failed(peer.ID)   {}

// discovery.Static answers an empty set with an error, but the Resolver
// interface permits "nil error, no peers" — a live Discovery whose want was
// satisfied by nothing — and that is a different branch.
func TestAnEmptyPeerSetWithNoErrorIsStillNoBackend(t *testing.T) {
	mn := newMocknet(t)
	p, err := NewPool(newHost(t, mn), &hookResolver{}, AuthProtocol, 4)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	require.ErrorIs(t, p.Do(t.Context(), func(*rpc.Client) error { return nil }), ErrNoBackend)
}

// The bound only bites when more peers ANSWER than it holds, which one Do
// never does — it stops at the first answer. Several Do calls against a
// rotating peer set is what fills it, and is what a running node does as
// discovery reorders behind it.
func TestTheOldestClientIsEvictedOnceTheBoundIsReached(t *testing.T) {
	mn := newMocknet(t)
	caller := newHost(t, mn)

	var ids []peer.ID
	for range 4 {
		ids = append(ids, newHost(t, mn).ID())
	}

	r := &hookResolver{peers: ids}
	p, err := NewPool(caller, r, AuthProtocol, 2)
	require.NoError(t, err)
	t.Cleanup(p.Close)

	for i := range ids {
		r.peers = append(ids[i:], ids[:i]...) // a different peer leads each time
		require.NoError(t, p.Do(t.Context(), func(*rpc.Client) error { return nil }))
		require.LessOrEqual(t, p.held(), 2, "the cache must never grow past its bound")
	}
	require.Equal(t, 2, p.held())
}
