package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
)

// fake is an instance with one peer, the handles it was told about, and its node's
// discovery over a finder naming the nodes a test gives it.
type fake struct {
	peer.Node
	*Served
	running []Running
	finding finding
}

func newFake(t *testing.T, mn *peer.Mocknet, found ...libp2ppeer.ID) *fake {
	t.Helper()
	p := mn.Mock(t.Context())
	t.Cleanup(p.Close)
	f := &fake{Node: p, finding: found}
	f.Served = NewServed(p.ID(), &f.finding, rpc.Retry{For: 50 * time.Millisecond})
	return f
}

func (f *fake) Peer() peer.Node    { return f.Node }
func (f *fake) Running() []Running { return f.running }
func (f *fake) Cloud() string      { return "test" }
func (*fake) Region() string       { return "" }
func (*fake) Now() time.Time       { return time.Now() }
func (f *fake) Root() string       { return "" }
func (f *fake) Config(any) error   { return nil }

var _ Host = (*fake)(nil)

type answer string

// store is a service answering what it holds.
type store struct{ held answer }

func (store) Start(context.Context, Host) error { return nil }
func (store) Close() error                      { return nil }

const storeProtocol = "/tau/store/v2"

func TestTheServiceThisNodeServesIsReachedWithNoClient(t *testing.T) {
	mn := peertest.Net(t)
	h := newFake(t, mn, "far")
	h.Serve(storeProtocol, store{held: "here"})

	dialled := 0
	r := Reach(h, storeProtocol, func(host.Host, libp2ppeer.ID) (store, error) { dialled++; return store{held: "far"}, nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("here"), got.held)
	require.Zero(t, dialled, "a client was dialled for a service this node serves")
	_, held := r.At()
	require.False(t, held)
}

func TestAServiceThisNodeServesIsReachedAsWhatItHolds(t *testing.T) {
	mn := peertest.Net(t)
	h := newFake(t, mn, "far")
	h.Serve(storeProtocol, store{held: "held"})

	dialled := 0
	r := ReachAs(h, storeProtocol, func(s store) answer { return s.held },
		func(host.Host, libp2ppeer.ID) (answer, error) { dialled++; return "far", nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("held"), got)
	require.Zero(t, dialled)

	elsewhere := newFake(t, mn, "far")
	got, err = ReachAs(elsewhere, storeProtocol, func(s store) answer { return s.held },
		func(host.Host, libp2ppeer.ID) (answer, error) { dialled++; return "far", nil }).Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("far"), got, "none here is a client of one elsewhere")
	require.Equal(t, 1, dialled)
}

func TestAServiceElsewhereIsDialledOnANodeDiscoveryNames(t *testing.T) {
	mn := peertest.Net(t)
	h := newFake(t, mn)
	h.finding = finding{h.ID(), "far"}

	var at libp2ppeer.ID
	r := Reach(h, storeProtocol, func(_ host.Host, id libp2ppeer.ID) (answer, error) { at = id; return "far", nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("far"), got)
	require.Equal(t, libp2ppeer.ID("far"), at, "never itself")
	require.Equal(t, []libp2ppeer.ID{"far"}, Serving(h, storeProtocol))

	h.finding = finding{h.ID()}
	nowhere := Reach(h, storeProtocol, func(host.Host, libp2ppeer.ID) (answer, error) { return "", nil })
	_, err = nowhere.Now(t.Context())
	require.ErrorIs(t, err, ErrNoneRuns)
}

func TestAServiceOfAnotherTypeIsNotTheOneReached(t *testing.T) {
	mn := peertest.Net(t)
	h := newFake(t, mn, "far")
	h.Serve(storeProtocol, &recorder{})

	var at libp2ppeer.ID
	got, err := Reach(h, storeProtocol, func(_ host.Host, id libp2ppeer.ID) (store, error) { at = id; return store{held: "far"}, nil }).Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("far"), got.held)
	require.Equal(t, libp2ppeer.ID("far"), at)
}

func TestANodeNamesItselfFirstForWhatItServes(t *testing.T) {
	mn := peertest.Net(t)
	h := newFake(t, mn, "far", "farther")
	require.Equal(t, []libp2ppeer.ID{"far", "farther"}, h.Find(storeProtocol).Known())

	h.Serve(storeProtocol, store{})
	found := h.Find(storeProtocol)
	require.Equal(t, []libp2ppeer.ID{h.ID(), "far", "farther"}, found.Known())
	first, err := found.Any(t.Context())
	require.NoError(t, err)
	require.Equal(t, h.ID(), first)
	two, err := found.Peers(t.Context(), 2)
	require.NoError(t, err)
	require.Equal(t, []libp2ppeer.ID{h.ID(), "far"}, two)
	one, err := found.Peers(t.Context(), 1)
	require.NoError(t, err)
	require.Equal(t, []libp2ppeer.ID{h.ID()}, one)

	local, ok := h.Local(storeProtocol)
	require.True(t, ok)
	require.Equal(t, store{}, local)
	_, ok = h.Local("/tau/other/v2")
	require.False(t, ok)
}

func TestANodeWithNoP2PFindsOnlyItself(t *testing.T) {
	var served Served
	require.Empty(t, served.Find(storeProtocol).Known())
	served.Serve(storeProtocol, store{})
	_, ok := served.Local(storeProtocol)
	require.True(t, ok)
}

func TestRoutedWorkLeavesForANodeDiscoveryNames(t *testing.T) {
	mn := peertest.Net(t)
	h := newFake(t, mn)
	h.finding = finding{h.ID(), "far"}

	var at libp2ppeer.ID
	r := Routed(h, "/tau/clock/v2", answer("here"), func(_ host.Host, id libp2ppeer.ID) (answer, error) { at = id; return "far", nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("far"), got, "another node, whenever one is found")
	require.Equal(t, libp2ppeer.ID("far"), at)

	h.finding = finding{h.ID()}
	got, err = r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("here"), got, "only this node found is this node, undialled")

	h.finding = nil
	got, err = r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("here"), got, "nobody found is nobody but this node")
}

// finding is a finder that finds these nodes for every protocol.
type finding []libp2ppeer.ID

func (f *finding) Find(protocol.ID) discovery.Resolver { return found(*f) }

// found is a resolver that knows these nodes.
type found []libp2ppeer.ID

func (f found) Known() []libp2ppeer.ID { return f }
func (f found) Any(ctx context.Context) (libp2ppeer.ID, error) {
	peers, err := f.Peers(ctx, 1)
	if err != nil {
		return "", err
	}
	return peers[0], nil
}
func (f found) Peers(context.Context, int) ([]libp2ppeer.ID, error) {
	if len(f) == 0 {
		return nil, discovery.ErrNoPeers
	}
	return f, nil
}
func (found) Failed(libp2ppeer.ID) {}

func TestNobodyFoundIsNobodyToAsk(t *testing.T) {
	mn := peertest.Net(t)
	h := newFake(t, mn)
	r := Reach(h, storeProtocol, func(host.Host, libp2ppeer.ID) (answer, error) { return "", nil })
	_, err := r.Now(t.Context())
	require.ErrorIs(t, err, ErrNoneRuns)
	require.Empty(t, Serving(h, storeProtocol))
}

// recorder is a service that keeps the host it started with.
type recorder struct {
	host Host
	err  error
}

func (r *recorder) Start(_ context.Context, h Host) error { r.host = h; return r.err }
func (r *recorder) Close() error                          { return nil }

func TestALonePeerRunsServicesBesideEachOther(t *testing.T) {
	mn := peertest.Net(t)
	p := mn.Mock(t.Context())
	t.Cleanup(p.Close)
	l := On(p, "cloud")

	first, second := &recorder{}, &recorder{}
	require.NoError(t, l.Start(t.Context(), "first", first))
	require.NoError(t, l.Start(t.Context(), "second", second))

	h := second.host
	require.Equal(t, p, h.Peer())
	require.Equal(t, "cloud", h.Cloud())
	require.Empty(t, h.Root())
	require.Empty(t, h.Region(), "a lone peer is in a cloud with no regions")
	require.WithinDuration(t, time.Now(), h.Now(), time.Minute)
	require.NoError(t, h.Config(new(answer)))
	got, ok := Get[*recorder](h)
	require.True(t, ok)
	require.Same(t, first, got, "what started before it")
	require.Len(t, h.Running(), 2)

	refused := &recorder{err: errors.New("no")}
	require.Error(t, l.Start(t.Context(), "refused", refused))
	require.Len(t, h.Running(), 2, "a service that failed to start is not running")
}

// serving is a service that serves storeProtocol on the node it starts on.
type serving struct{ store }

func (s serving) Start(_ context.Context, h Host) error {
	h.Serve(storeProtocol, s.store)
	return nil
}

func TestAServiceOnALonePeerIsReachedByTheNextWithNoClient(t *testing.T) {
	mn := peertest.Net(t)
	p := mn.Mock(t.Context())
	t.Cleanup(p.Close)
	l := On(p, "cloud")
	require.NoError(t, l.Start(t.Context(), "store", serving{store{held: "lone"}}))

	next := &recorder{}
	require.NoError(t, l.Start(t.Context(), "next", next))
	got, err := Reach(next.host, storeProtocol, func(host.Host, libp2ppeer.ID) (store, error) {
		t.Fatal("a client was dialled for a service on the same peer")
		return store{}, nil
	}).Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("lone"), got.held)
	require.Equal(t, []libp2ppeer.ID{p.ID()}, next.host.Find(storeProtocol).Known())
}

func TestANilDiscoveryFindsNothingAndKeepsNothing(t *testing.T) {
	var served *Served
	served.Serve(storeProtocol, store{})
	_, ok := served.Local(storeProtocol)
	require.False(t, ok)
	require.Empty(t, served.Find(storeProtocol).Known())
}

func TestTheFirstToServeAProtocolIsWhatTheNodeServesItWith(t *testing.T) {
	mn := peertest.Net(t)
	h := newFake(t, mn)
	h.Serve(storeProtocol, store{held: "mocked"})
	h.Serve(storeProtocol, store{held: "shipped"})
	got, ok := Local[store](h, storeProtocol)
	require.True(t, ok)
	require.Equal(t, answer("mocked"), got.held, "a later service replaced what the composer served first")
}
