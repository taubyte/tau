package discovery_test

import (
	"context"
	"sync"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	libdisc "github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	netmock "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
)

const testProtocol = protocol.ID("/tau/test/discovery/v1")

// tick is the retry interval every test runs at. The property under test is
// that there IS a bound, not what it is.
const tick = 50 * time.Millisecond

func newHost(t testing.TB) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { h.Close() })
	return h
}

func connectHosts(t testing.TB, from, to host.Host) {
	t.Helper()
	from.Peerstore().AddAddrs(to.ID(), to.Addrs(), time.Hour)
	require.NoError(t, from.Connect(t.Context(), peer.AddrInfo{ID: to.ID(), Addrs: to.Addrs()}))
}

// serve makes h answer on the protocol, which is what identify tells its peers
// about.
func serve(t testing.TB, h host.Host) {
	t.Helper()
	h.SetStreamHandler(testProtocol, func(s network.Stream) { s.Close() })
}

func newDiscovery(t *testing.T, h host.Host, opts ...discovery.Option) *discovery.Discovery {
	t.Helper()
	d, err := discovery.New(t.Context(), h, testProtocol,
		append([]discovery.Option{discovery.WithRetryInterval(tick)}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(d.Close)
	return d
}

// remember writes what a peerstore holds about a node nothing has connected
// to: real addresses, and the protocol it answers. It is how a node knows a
// peer it has not met — a record left by a previous session, or written by
// another component — and it is the source no event will ever fire for.
func remember(from host.Host, about host.Host, ttl time.Duration) {
	from.Peerstore().AddAddrs(about.ID(), about.Addrs(), ttl)
	from.Peerstore().AddProtocols(about.ID(), testProtocol)
}

func within(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

// fakeRouting stands in for the node's DHT. Routing is injected because a
// host.Host has none, and because the real one does not work under mocknet and
// is minutes-grained in production — neither of which a test can wait for.
type fakeRouting struct {
	mu      sync.Mutex
	asked   []string
	results [][]peer.AddrInfo // per call; the last one repeats
	err     error
}

func (f *fakeRouting) FindPeers(ctx context.Context, ns string, opts ...libdisc.Option) (<-chan peer.AddrInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.asked = append(f.asked, ns)
	if f.err != nil {
		return nil, f.err
	}

	var out []peer.AddrInfo
	if n := len(f.results); n > 0 {
		out = f.results[min(len(f.asked)-1, n-1)]
	}

	ch := make(chan peer.AddrInfo, len(out))
	for _, ai := range out {
		ch <- ai
	}
	close(ch)
	return ch, nil
}

func (f *fakeRouting) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

func TestNewRejectsWhatItCannotWatch(t *testing.T) {
	h := newHost(t)

	for _, c := range []struct {
		name string
		host host.Host
		id   protocol.ID
		opt  discovery.Option
	}{
		{name: "no host", id: testProtocol},
		{name: "no protocol", host: h},
		{name: "retry interval of zero", host: h, id: testProtocol, opt: discovery.WithRetryInterval(0)},
		{name: "negative retry interval", host: h, id: testProtocol, opt: discovery.WithRetryInterval(-time.Second)},
		{name: "a cap of nothing", host: h, id: testProtocol, opt: discovery.WithMaxPeers(0)},
		{name: "a lookup limit of nothing", host: h, id: testProtocol, opt: discovery.WithLookupLimit(0)},
	} {
		t.Run(c.name, func(t *testing.T) {
			var opts []discovery.Option
			if c.opt != nil {
				opts = append(opts, c.opt)
			}
			d, err := discovery.New(t.Context(), c.host, c.id, opts...)
			require.Error(t, err)
			assert.Nil(t, d)
		})
	}
}

func TestAskingBeforeAnyPeerIsUp(t *testing.T) {
	d := newDiscovery(t, newHost(t))

	_, err := d.Any(within(t, 150*time.Millisecond))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	// The deadline is part of the answer: "none yet" and "you gave up" are
	// the same sentence, and a caller retrying needs to know which.
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, d.Known())
}

func TestAPeerThatStartsServingIsFound(t *testing.T) {
	server, client := newHost(t), newHost(t)
	connectHosts(t, client, server)

	d := newDiscovery(t, client)

	// Connected, but serving nothing yet.
	_, err := d.Any(within(t, 200*time.Millisecond))
	require.ErrorIs(t, err, discovery.ErrNoPeers)

	serve(t, server)

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, server.ID(), pid)
	assert.Equal(t, []peer.ID{server.ID()}, d.Known())
}

func TestAPeerAlreadyServingWhenWatchingStarts(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, client, server)

	// Identification is already over by the time this Discovery exists, so
	// there is no event left to fire: the opening scan is what finds it.
	d := newDiscovery(t, client)

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, server.ID(), pid)
}

func TestAPeerThatStopsServingLeavesTheSet(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, client, server)

	d := newDiscovery(t, client)
	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	require.Equal(t, server.ID(), pid)

	server.RemoveStreamHandler(testProtocol)

	require.Eventually(t, func() bool { return len(d.Known()) == 0 }, 5*time.Second, 10*time.Millisecond,
		"a peer that stopped answering is still being handed out")

	_, err = d.Any(within(t, 200*time.Millisecond))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
}

func TestAPeerThatGoesAwayLeavesTheSet(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, client, server)

	// A long retry here so the assertion below is about the disconnect being
	// noticed, not about how soon a peer that only looks dead is allowed back.
	d := newDiscovery(t, client, discovery.WithRetryInterval(30*time.Second))
	_, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)

	require.NoError(t, server.Close())

	require.Eventually(t, func() bool { return len(d.Known()) == 0 }, 5*time.Second, 10*time.Millisecond,
		"a peer whose host is gone is still being handed out")
}

func TestAskingForMorePeersThanExist(t *testing.T) {
	one, two, client := newHost(t), newHost(t), newHost(t)
	serve(t, one)
	serve(t, two)
	connectHosts(t, client, one)
	connectHosts(t, client, two)

	d := newDiscovery(t, client)

	require.Eventually(t, func() bool { return len(d.Known()) == 2 }, 5*time.Second, 10*time.Millisecond)

	// Five asked for, two in the swarm: the deadline is what tells you so.
	start := time.Now()
	ids, err := d.Peers(within(t, 300*time.Millisecond), 5)
	require.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
	assert.ElementsMatch(t, []peer.ID{one.ID(), two.ID()}, ids)

	// Two asked for, two there: no waiting at all.
	start = time.Now()
	ids, err = d.Peers(within(t, 5*time.Second), 2)
	require.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.Less(t, time.Since(start), time.Second)
}

func TestAskingForMoreThanTheCapIsRefusedAtOnce(t *testing.T) {
	d := newDiscovery(t, newHost(t), discovery.WithMaxPeers(2))

	// A wait that could never end is an error, not a wait.
	start := time.Now()
	_, err := d.Peers(within(t, 5*time.Second), 3)
	require.Error(t, err)
	assert.NotErrorIs(t, err, discovery.ErrNoPeers)
	assert.Less(t, time.Since(start), time.Second)

	_, err = d.Peers(t.Context(), 0)
	require.Error(t, err)
}

func TestFailedTakesAPeerOutAndTheRetryPutsItBack(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, client, server)

	d := newDiscovery(t, client)
	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)

	d.Failed(pid)
	assert.Empty(t, d.Known())

	// Out for the retry interval, so a caller looping on a broken peer does
	// not get the same one straight back.
	_, err = d.Any(within(t, tick/2))
	require.ErrorIs(t, err, discovery.ErrNoPeers)

	// And back afterwards, because a failed dial is nearly always temporary.
	// A blacklist would leave this empty forever.
	back, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, pid, back)
}

// TestTheRetryReissuesALookupThatCameBackEmpty is the first half of the
// property the retry ticker exists for. In production the node's routing sits
// behind libp2p's backoff cache, whose floor is a minute: a lookup that found
// nothing is answered instantly, from that same nothing, for the whole window.
// Nothing in this package is event-driven for a peer it has never connected
// to, so a request parked behind that first empty answer is served by the
// ticker or by nothing at all.
func TestTheRetryReissuesALookupThatCameBackEmpty(t *testing.T) {
	// The clock this bounds is the caller's deadline, so the default has to
	// stay far below any of them.
	require.LessOrEqual(t, discovery.DefaultRetryInterval, time.Second)

	elsewhere, client := newHost(t), newHost(t)
	routing := &fakeRouting{results: [][]peer.AddrInfo{
		{}, // the poisoned answer
		{{ID: elsewhere.ID(), Addrs: elsewhere.Addrs()}},
	}}

	d := newDiscovery(t, client, discovery.WithRouting(routing))

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, elsewhere.ID(), pid)
	assert.GreaterOrEqual(t, len(routing.calls()), 2, "the empty lookup was never asked again")

	// Routing named it, so it can be dialled — but discovery did not dial it,
	// and the addresses it needs are where the caller's dial will look.
	assert.Equal(t, network.NotConnected, client.Network().Connectedness(pid))
	assert.NotEmpty(t, client.Peerstore().Addrs(pid))
}

// TestTheRetryRescansThePeerstore is the second half. Here routing is poisoned
// for good, and the peer arrives the way v1's investigation described it: in
// the peerstore, while the request was already parked, with no event to
// announce it because nothing is connected to it. Without the ticker's rescan
// this request waits out its whole deadline beside an answer that is already
// sitting in the peerstore.
func TestTheRetryRescansThePeerstore(t *testing.T) {
	elsewhere, client := newHost(t), newHost(t)
	routing := &fakeRouting{} // every lookup empty, forever

	d := newDiscovery(t, client, discovery.WithRouting(routing))

	type answer struct {
		pid peer.ID
		err error
	}
	answers := make(chan answer, 1)
	go func() {
		pid, err := d.Any(within(t, 5*time.Second))
		answers <- answer{pid, err}
	}()

	// Park first, arrive second.
	time.Sleep(4 * tick)
	remember(client, elsewhere, time.Hour)

	select {
	case a := <-answers:
		require.NoError(t, a.err)
		assert.Equal(t, elsewhere.ID(), a.pid)
	case <-time.After(5 * time.Second):
		t.Fatal("a peer sitting in the peerstore never reached a parked request")
	}
	assert.NotEmpty(t, routing.calls())
}

// TestKnownStaysHonestWithNobodyWaiting covers the half of the tick that is
// not conditional. Rescanning the world is only worth doing for a request that
// wants more, but re-reading the members is what keeps the set from holding
// peers nothing can reach any more.
func TestKnownStaysHonestWithNobodyWaiting(t *testing.T) {
	elsewhere, client := newHost(t), newHost(t)
	remember(client, elsewhere, 300*time.Millisecond)

	d := newDiscovery(t, client)

	require.Eventually(t, func() bool { return len(d.Known()) == 1 }, 5*time.Second, 10*time.Millisecond)

	// Its last address expires, so nothing can dial it, and nobody is asking.
	require.Eventually(t, func() bool { return len(d.Known()) == 0 }, 5*time.Second, 10*time.Millisecond,
		"an unreachable peer stayed in the set because nobody happened to ask")
}

func TestKnownPutsConnectedPeersFirst(t *testing.T) {
	connected, elsewhere, client := newHost(t), newHost(t), newHost(t)
	serve(t, connected)
	connectHosts(t, client, connected)
	remember(client, elsewhere, time.Hour)

	d := newDiscovery(t, client)

	require.Eventually(t, func() bool { return len(d.Known()) == 2 }, 5*time.Second, 10*time.Millisecond)

	ids := d.Known()
	// The first id a caller takes should be the one that needs no dial.
	assert.Equal(t, connected.ID(), ids[0])
	assert.Equal(t, elsewhere.ID(), ids[1])

	pid, err := d.Any(within(t, time.Second))
	require.NoError(t, err)
	assert.Equal(t, connected.ID(), pid)
}

func TestARoutingResultThatDoesNotServeIsThrownOut(t *testing.T) {
	other, client := newHost(t), newHost(t)
	// It answers something, just not this.
	other.SetStreamHandler("/tau/test/discovery/other", func(s network.Stream) { s.Close() })
	connectHosts(t, client, other)

	routing := &fakeRouting{results: [][]peer.AddrInfo{{{ID: other.ID(), Addrs: other.Addrs()}}}}
	d := newDiscovery(t, client, discovery.WithRouting(routing))

	// Identify has the truth, and it outranks the advertisement.
	_, err := d.Any(within(t, 500*time.Millisecond))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
}

func TestALookupThatFailsIsJustAnEmptyRound(t *testing.T) {
	client := newHost(t)
	routing := &fakeRouting{err: assertError{}}

	d := newDiscovery(t, client, discovery.WithRouting(routing))

	_, err := d.Any(within(t, 300*time.Millisecond))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	assert.GreaterOrEqual(t, len(routing.calls()), 2, "a failing lookup stopped the retry")
}

type assertError struct{}

func (assertError) Error() string { return "routing is down" }

func TestTheCapBoundsTheSet(t *testing.T) {
	one, two, client := newHost(t), newHost(t), newHost(t)
	remember(client, one, time.Hour)
	remember(client, two, time.Hour)

	d := newDiscovery(t, client, discovery.WithMaxPeers(1))

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Contains(t, []peer.ID{one.ID(), two.ID()}, pid)

	time.Sleep(4 * tick)
	assert.Len(t, d.Known(), 1)
}

func TestAdvertiseAndLookupUseTheSameName(t *testing.T) {
	adv := &fakeAdvertiser{ttl: time.Hour}
	discovery.Advertise(t.Context(), adv, testProtocol)

	client := newHost(t)
	routing := &fakeRouting{}
	d := newDiscovery(t, client, discovery.WithRouting(routing))
	_, err := d.Any(within(t, 200*time.Millisecond))
	require.ErrorIs(t, err, discovery.ErrNoPeers)

	require.Eventually(t, func() bool { return len(adv.names()) > 0 }, 5*time.Second, 10*time.Millisecond)
	require.NotEmpty(t, routing.calls())

	// The half that publishes and the half that searches have to agree, and
	// neither errors if they do not.
	assert.Equal(t, adv.names()[0], routing.calls()[0])
	assert.Equal(t, string(testProtocol), adv.names()[0])
}

func TestAdvertiseWithNothingToAdvertiseOn(t *testing.T) {
	// Neither is worth a panic, and neither is worth an error a caller has to
	// handle at start-up.
	discovery.Advertise(t.Context(), nil, testProtocol)
	discovery.Advertise(t.Context(), &fakeAdvertiser{ttl: time.Hour}, "")
}

type fakeAdvertiser struct {
	mu    sync.Mutex
	asked []string
	ttl   time.Duration
}

func (f *fakeAdvertiser) Advertise(ctx context.Context, ns string, opts ...libdisc.Option) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, ns)
	return f.ttl, nil
}

func (f *fakeAdvertiser) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

func TestClosing(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, client, server)

	d := newDiscovery(t, client)
	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, testProtocol, d.Protocol())

	// A member is held against the connection manager's trim while it is one,
	// and released when it stops being one.
	assert.True(t, isProtected(client, pid))

	d.Close()
	d.Close() // idempotent, and the second one does not wait forever

	assert.False(t, isProtected(client, pid))
	assert.Empty(t, d.Known())

	_, err = d.Any(t.Context())
	require.ErrorIs(t, err, discovery.ErrClosed)
	_, err = d.Peers(t.Context(), 1)
	require.ErrorIs(t, err, discovery.ErrClosed)
}

func TestClosingWakesAParkedRequest(t *testing.T) {
	d := newDiscovery(t, newHost(t))

	errs := make(chan error, 1)
	go func() {
		_, err := d.Any(t.Context())
		errs <- err
	}()

	time.Sleep(2 * tick)
	d.Close()

	select {
	case err := <-errs:
		require.ErrorIs(t, err, discovery.ErrClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("a request parked when the loop stopped never came back")
	}
}

func TestWatchingEndsWithTheContextItWasGiven(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	d, err := discovery.New(ctx, newHost(t), testProtocol, discovery.WithRetryInterval(tick))
	require.NoError(t, err)

	cancel()

	require.Eventually(t, func() bool {
		_, err := d.Any(t.Context())
		return err != nil
	}, 5*time.Second, 10*time.Millisecond)
	d.Close()
}

func TestStaticResolvesWhatItWasGiven(t *testing.T) {
	one, two := newHost(t).ID(), newHost(t).ID()
	r := discovery.Static(one, two)

	pid, err := r.Any(t.Context())
	require.NoError(t, err)
	assert.Equal(t, one, pid)

	assert.Equal(t, []peer.ID{one, two}, r.Known())

	ids, err := r.Peers(t.Context(), 1)
	require.NoError(t, err)
	assert.Equal(t, []peer.ID{one}, ids)

	// Nothing can change, so asking for more than it holds is answered at
	// once rather than waited out.
	start := time.Now()
	ids, err = r.Peers(within(t, 5*time.Second), 9)
	require.NoError(t, err)
	assert.Equal(t, []peer.ID{one, two}, ids)
	assert.Less(t, time.Since(start), time.Second)

	_, err = r.Peers(t.Context(), 0)
	require.Error(t, err)

	// A configured list stays what it was configured as.
	r.Failed(one)
	assert.Equal(t, []peer.ID{one, two}, r.Known())
}

func TestStaticWithNothingInIt(t *testing.T) {
	r := discovery.Static()

	_, err := r.Any(t.Context())
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	assert.Empty(t, r.Known())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = discovery.Static(newHost(t).ID()).Peers(ctx, 1)
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	require.ErrorIs(t, err, context.Canceled)
}

func isProtected(h host.Host, pid peer.ID) bool {
	return h.ConnManager().IsProtected(pid, "")
}

// TestAPeerThatComesBackIsTrustedAtOnce is the other half of the doubt a lost
// connection raises. Waiting out the interval would be right for a peer that
// merely stopped answering, and wrong here: the connection is the evidence,
// and it has arrived.
func TestAPeerThatComesBackIsTrustedAtOnce(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, client, server)

	// Long enough that nothing below can be the retry quietly doing the work.
	d := newDiscovery(t, client, discovery.WithRetryInterval(30*time.Second))
	_, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)

	require.NoError(t, client.Network().ClosePeer(server.ID()))
	require.Eventually(t, func() bool { return len(d.Known()) == 0 }, 5*time.Second, 10*time.Millisecond)

	connectHosts(t, client, server)

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, server.ID(), pid)
}

// TestFindingAPeerUnderMocknet is the harness every v2 service's wire test
// runs in: no ports, no DHT, nothing published. The peerstore half has to work
// there, or a service that resolves a peer before calling it cannot be tested
// without a universe.
func TestFindingAPeerUnderMocknet(t *testing.T) {
	mn := netmock.New()
	t.Cleanup(func() { mn.Close() })

	server, err := mn.GenPeer()
	require.NoError(t, err)
	client, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())

	serve(t, server)
	require.NoError(t, client.Connect(t.Context(), peer.AddrInfo{ID: server.ID(), Addrs: server.Addrs()}))

	d, err := discovery.New(t.Context(), client, testProtocol, discovery.WithRetryInterval(tick))
	require.NoError(t, err)
	t.Cleanup(d.Close)

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, server.ID(), pid)
}
