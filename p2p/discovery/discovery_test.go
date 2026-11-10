package discovery_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	libdisc "github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	netmock "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/discovery"
)

const testProtocol = protocol.ID("/tau/test/discovery/v1")

// tick is a fast retry interval and noTick one no test can reach, to take the ticker
// out of what a test measures. A shorter interval is allowed only where it makes the
// assertion harder to pass or cannot reach it; a test whose property is served by a
// tick runs at DefaultRetryInterval (atProductionSpeed).
const (
	tick   = discovery.DefaultRetryInterval / 20
	noTick = 30 * time.Second
)

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

// atProductionSpeed builds one running at the interval that ships, for the tests
// whose property is that interval.
func atProductionSpeed(t *testing.T, h host.Host, opts ...discovery.Option) *discovery.Discovery {
	t.Helper()
	return newDiscovery(t, h,
		append([]discovery.Option{discovery.WithRetryInterval(discovery.DefaultRetryInterval)}, opts...)...)
}

// remember writes what a peerstore holds about a node nothing has connected to: real
// addresses and the protocol it answers. No event ever fires for it.
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

// fakeRouting stands in for the node's DHT, which does not work under mocknet and is
// minutes-grained in production.
type fakeRouting struct {
	mu      sync.Mutex
	asked   []string
	results [][]peer.AddrInfo // per call; the last one repeats
	err     error
	// block, when set, holds a lookup in flight the way a real DHT query is
	// in flight for seconds at a time. It is closed to let them all through.
	block chan struct{}
}

func (f *fakeRouting) FindPeers(ctx context.Context, ns string, opts ...libdisc.Option) (<-chan peer.AddrInfo, error) {
	f.mu.Lock()
	f.asked = append(f.asked, ns)
	block, err := f.block, f.err
	f.mu.Unlock()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if err != nil {
		return nil, err
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

	type testCase struct {
		name string
		host host.Host
		id   protocol.ID
		opt  discovery.Option
	}
	for _, c := range []testCase{
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

// listed is what from's peerstore says about serves.
func listed(t *testing.T, from host.Host, about peer.ID) []protocol.ID {
	t.Helper()
	protos, err := from.Peerstore().GetProtocols(about)
	require.NoError(t, err)
	return protos
}

// serves says whether from's peerstore lists about as serving the protocol.
func serves(from host.Host, about peer.ID) bool {
	protos, err := from.Peerstore().GetProtocols(about)
	return err == nil && slices.Contains(protos, testProtocol)
}

func leftOut(protos []protocol.ID, id protocol.ID) []protocol.ID {
	return slices.DeleteFunc(slices.Clone(protos), func(p protocol.ID) bool { return p == id })
}

// identifiedOnce waits until identify has told from what about serves.
func identifiedOnce(t *testing.T, from host.Host, about peer.ID) {
	t.Helper()
	require.Eventually(t, func() bool {
		protos, err := from.Peerstore().GetProtocols(about)
		return err == nil && len(protos) > 0
	}, 5*time.Second, 10*time.Millisecond, "identify never told the client what the server serves")
}

// naming is routing that names about, at the addresses it listens on.
func naming(about host.Host) *fakeRouting {
	return &fakeRouting{results: [][]peer.AddrInfo{{{ID: about.ID(), Addrs: about.Addrs()}}}}
}

// THE LIST NEVER EXPIRES, so a peer that began to serve after its connection closed
// is still denied by what it said before, and the push that would correct it had
// nobody to go to. Routing naming it is not overruled by that list: it is dialled
// and asked, since no caller connects to a peer it is never handed.
func TestAPeerRoutingNamesThatAnOldListDeniesIsDialledAndAsked(t *testing.T) {
	server, client := newHost(t), newHost(t)
	connectHosts(t, client, server)
	identifiedOnce(t, client, server.ID())
	require.NoError(t, client.Network().ClosePeer(server.ID()))
	require.Eventually(t, func() bool { return client.Network().Connectedness(server.ID()) != network.Connected },
		5*time.Second, 10*time.Millisecond)
	serve(t, server)

	d := newDiscovery(t, client, discovery.WithRouting(naming(server)))

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, server.ID(), pid)
	assert.True(t, serves(client, server.ID()))
}

func TestAPeerRoutingNamesThatCannotBeReachedIsNotHandedOut(t *testing.T) {
	server, client := newHost(t), newHost(t)
	connectHosts(t, client, server)
	identifiedOnce(t, client, server.ID())
	addrs := server.Addrs()
	require.NoError(t, server.Close())
	require.Eventually(t, func() bool { return client.Network().Connectedness(server.ID()) != network.Connected },
		5*time.Second, 10*time.Millisecond)

	routing := &fakeRouting{results: [][]peer.AddrInfo{{{ID: server.ID(), Addrs: addrs}}}}
	d := newDiscovery(t, client, discovery.WithRouting(routing))

	_, err := d.Any(within(t, 500*time.Millisecond))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
}

// A PEER THAT REFUSES WHEN ASKED has said what identify said, and is not asked again
// for each lookup that names it.
func TestAPeerThatRefusesWhenAskedIsAskedOnce(t *testing.T) {
	server, client := newHost(t), newHost(t)
	var asked atomic.Int32
	server.Mux().AddHandlerWithFunc("/tau/test/discovery/counts", func(p protocol.ID) bool {
		if p == testProtocol {
			asked.Add(1)
		}
		return false
	}, nil)
	connectHosts(t, client, server)
	identifiedOnce(t, client, server.ID())
	routing := naming(server)
	d := newDiscovery(t, client, discovery.WithRouting(routing))

	_, err := d.Any(within(t, 20*tick))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	assert.GreaterOrEqual(t, len(routing.calls()), 2, "routing named the server only once")
	assert.Equal(t, int32(1), asked.Load(), "a peer that refused was asked again")
}

// flaky is a host whose first fail streams fail before they negotiate, the way a
// stream to an overloaded peer is reset.
type flaky struct {
	host.Host
	fail  atomic.Int32
	tries atomic.Int32
}

func (f *flaky) NewStream(ctx context.Context, p peer.ID, pids ...protocol.ID) (network.Stream, error) {
	f.tries.Add(1)
	if f.fail.Add(-1) >= 0 {
		return nil, errors.New("stream reset")
	}
	return f.Host.NewStream(ctx, p, pids...)
}

// staleAbout is a client whose list says the server does not serve while it does,
// over a flaky host that fails its first fail streams.
func staleAbout(t *testing.T, fail int32) (server host.Host, client *flaky) {
	t.Helper()
	server, h := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, h, server)
	require.Eventually(t, func() bool { return serves(h, server.ID()) }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, h.Peerstore().SetProtocols(server.ID(), leftOut(listed(t, h, server.ID()), testProtocol)...))

	client = &flaky{Host: h}
	client.fail.Store(fail)
	return server, client
}

func TestAPeerThatFailsToAnswerIsAskedAgain(t *testing.T) {
	server, client := staleAbout(t, 2)
	d := newDiscovery(t, client, discovery.WithRouting(naming(server)))

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, server.ID(), pid)
	assert.Equal(t, int32(3), client.tries.Load(), "asked other than until it answered")
}

func TestClosingEndsAnAsk(t *testing.T) {
	server, client := staleAbout(t, 1<<30)
	d := newDiscovery(t, client, discovery.WithRouting(naming(server)))

	go d.Any(within(t, 5*time.Second))
	require.Eventually(t, func() bool { return client.tries.Load() >= 2 }, 5*time.Second, 10*time.Millisecond,
		"the server was never asked again")

	d.Close()
	time.Sleep(2 * tick)
	tries := client.tries.Load()
	time.Sleep(10 * tick)
	assert.Equal(t, tries, client.tries.Load(), "an ask outlived its Discovery")
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

	// How long a failed peer is held out is the retry interval, so this runs at the
	// shipped one.
	d := atProductionSpeed(t, client)
	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)

	d.Failed(pid)
	assert.Empty(t, d.Known())

	// Out for the interval, so a caller looping on a broken peer does not get
	// the same one straight back.
	_, err = d.Any(within(t, discovery.DefaultRetryInterval/2))
	require.ErrorIs(t, err, discovery.ErrNoPeers)

	// And back afterwards, because a failed dial is nearly always temporary.
	// A blacklist would leave this empty forever.
	back, err := d.Any(within(t, 5*discovery.DefaultRetryInterval))
	require.NoError(t, err)
	assert.Equal(t, pid, back)
}

// Serving reads Known and never waits, so a peer held out for an interval must
// come back on its own once the interval ends.
func TestAFailedPeerComesBackWithNobodyWaiting(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, client, server)

	d := atProductionSpeed(t, client)
	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)

	d.Failed(pid)
	require.Empty(t, d.Known())
	require.Eventually(t, func() bool { return slices.Equal(d.Known(), []peer.ID{pid}) },
		5*discovery.DefaultRetryInterval, 10*time.Millisecond,
		"a peer that failed once stayed out for a caller that only reads what is known")
}

func TestTheRetryReissuesALookupThatCameBackEmpty(t *testing.T) {
	// The clock this bounds is the caller's deadline, so the default has to
	// stay far below any of them.
	require.LessOrEqual(t, discovery.DefaultRetryInterval, time.Second)

	elsewhere, client := newHost(t), newHost(t)
	routing := &fakeRouting{results: [][]peer.AddrInfo{
		{}, // the poisoned answer
		{{ID: elsewhere.ID(), Addrs: elsewhere.Addrs()}},
	}}

	// At the shipped interval, so the second lookup is measured against what a caller
	// pays.
	d := atProductionSpeed(t, client, discovery.WithRouting(routing))

	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, elsewhere.ID(), pid)
	assert.GreaterOrEqual(t, len(routing.calls()), 2, "the empty lookup was never asked again")

	// Routing named it, so it can be dialled — but discovery did not dial it,
	// and the addresses it needs are where the caller's dial will look.
	assert.Equal(t, network.NotConnected, client.Network().Connectedness(pid))
	assert.NotEmpty(t, client.Peerstore().Addrs(pid))
}

func TestTheRetryRescansThePeerstore(t *testing.T) {
	elsewhere, client := newHost(t), newHost(t)
	routing := &fakeRouting{} // every lookup empty, forever

	d := atProductionSpeed(t, client, discovery.WithRouting(routing))

	type answer struct {
		pid peer.ID
		err error
	}
	answers := make(chan answer, 1)
	go func() {
		pid, err := d.Any(within(t, 5*time.Second))
		answers <- answer{pid, err}
	}()

	// Park first, arrive second, well inside the first interval, so a tick serves the
	// request.
	time.Sleep(discovery.DefaultRetryInterval / 5)
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

func TestKnownStaysHonestWithNobodyWaiting(t *testing.T) {
	elsewhere, client := newHost(t), newHost(t)
	remember(client, elsewhere, discovery.DefaultRetryInterval/3)

	// The reap is the tick, so this runs at the shipped interval.
	d := atProductionSpeed(t, client)

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

	// The first id should need no dial. Map iteration is random, so twenty looks make
	// it evidence.
	for range 20 {
		ids := d.Known()
		require.Len(t, ids, 2)
		require.Equal(t, connected.ID(), ids[0])
		require.Equal(t, elsewhere.ID(), ids[1])
	}

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

	d := atProductionSpeed(t, client, discovery.WithRouting(routing))

	// One interval and a half: the look on the way in, and at least one tick after the
	// error.
	_, err := d.Any(within(t, 3*discovery.DefaultRetryInterval/2))
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

func TestANodeDoesNotDiscoverItself(t *testing.T) {
	client := newHost(t)
	serve(t, client) // it serves the protocol it is looking for

	routing := &fakeRouting{results: [][]peer.AddrInfo{{{ID: client.ID(), Addrs: client.Addrs()}}}}
	d := newDiscovery(t, client, discovery.WithRouting(routing))

	_, err := d.Any(within(t, 10*tick))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	assert.Empty(t, d.Known())
	require.NotEmpty(t, routing.calls(), "routing was never asked, so nothing was refused")
}

func TestARecordThatNamesNoProtocolIsNotAPeer(t *testing.T) {
	stranger, client := newHost(t), newHost(t)
	// Addresses and nothing else. Nowhere does this say it serves anything.
	client.Peerstore().AddAddrs(stranger.ID(), stranger.Addrs(), time.Hour)

	d := newDiscovery(t, client)

	_, err := d.Any(within(t, 10*tick))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	assert.Empty(t, d.Known())

	// The control: the same peer, once identify's answer is there.
	client.Peerstore().AddProtocols(stranger.ID(), testProtocol)
	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, stranger.ID(), pid)
}

func TestALiveConnectionOutranksTheAddressBook(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)
	connectHosts(t, client, server)

	d := newDiscovery(t, client)
	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)

	client.Peerstore().ClearAddrs(pid)
	require.Empty(t, client.Peerstore().Addrs(pid), "the address book still holds it, so the test proves nothing")
	require.Equal(t, network.Connected, client.Network().Connectedness(pid))

	// Several reaps at the fast interval, which is the harder way to ask:
	// every one of them is a chance to drop it.
	time.Sleep(10 * tick)
	assert.Equal(t, []peer.ID{pid}, d.Known(), "a peer we are connected to was dropped for having no address record")
}

// spyStore is a peerstore that counts what it was asked and can refuse to answer:
// "the store is down" must not read as "this peer serves the protocol".
type spyStore struct {
	peerstore.Peerstore

	mu    sync.Mutex
	asked map[peer.ID]int
	err   error
}

func (s *spyStore) GetProtocols(p peer.ID) ([]protocol.ID, error) {
	s.mu.Lock()
	s.asked[p]++
	err := s.err
	s.mu.Unlock()

	if err != nil {
		return nil, err
	}
	return s.Peerstore.GetProtocols(p)
}

func (s *spyStore) failWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *spyStore) count(ids ...peer.ID) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	var n int
	for _, id := range ids {
		n += s.asked[id]
	}
	return n
}

// spied hands this package a different peerstore than the host's own; everything
// goes through host.Peerstore(), so this is the whole seam.
type spied struct {
	host.Host
	store *spyStore
}

func (h spied) Peerstore() peerstore.Peerstore { return h.store }

func spy(h host.Host) (host.Host, *spyStore) {
	s := &spyStore{Peerstore: h.Peerstore(), asked: make(map[peer.ID]int)}
	return spied{Host: h, store: s}, s
}

func TestAPeerstoreThatCannotAnswerIsNotAYes(t *testing.T) {
	elsewhere, client := newHost(t), newHost(t)
	remember(client, elsewhere, time.Hour) // a record that is admitted below

	h, store := spy(client)
	store.failWith(errors.New("peerstore is unavailable"))

	d := newDiscovery(t, h)

	_, err := d.Any(within(t, 10*tick))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	require.NotZero(t, store.count(elsewhere.ID()), "the peerstore was never asked, so the test proves nothing")

	// The control: the same record, once the store can answer again.
	store.failWith(nil)
	pid, err := d.Any(within(t, 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, elsewhere.ID(), pid)
}

// alreadyScanned starts a Discovery over a peerstore holding one peer and returns
// once that peer is in the set, the only reliable sign the opening scan is done.
// A record written after this is one no scan has seen and no event will fire for.
// The ticker is out of reach.
func alreadyScanned(t *testing.T, client host.Host, opts ...discovery.Option) *discovery.Discovery {
	t.Helper()

	remember(client, newHost(t), time.Hour)
	d := newDiscovery(t, client, append([]discovery.Option{discovery.WithRetryInterval(noTick)}, opts...)...)
	require.Eventually(t, func() bool { return len(d.Known()) == 1 }, 5*time.Second, 10*time.Millisecond,
		"the opening scan never ran, so what follows would prove nothing")
	return d
}

func TestARequestLooksAtOnceRatherThanWaitingForTheTick(t *testing.T) {
	elsewhere, client := newHost(t), newHost(t)

	d := alreadyScanned(t, client)
	remember(client, elsewhere, time.Hour)

	// Asking for two is what parks the request: one is already known, so this
	// is short by exactly the peer that arrived after the last scan.
	start := time.Now()
	ids, err := d.Peers(within(t, 5*time.Second), 2)
	require.NoError(t, err)
	require.Len(t, ids, 2, "the request sat until its deadline and came back with what it started with")
	assert.Contains(t, ids, elsewhere.ID())
	assert.Less(t, time.Since(start), discovery.DefaultRetryInterval,
		"the request waited for a tick instead of looking for itself")
}

func TestRoutingIsNotAskedForAnAnswerThePeerstoreHas(t *testing.T) {
	elsewhere, client := newHost(t), newHost(t)
	routing := &fakeRouting{}

	d := alreadyScanned(t, client, discovery.WithRouting(routing))
	remember(client, elsewhere, time.Hour)

	ids, err := d.Peers(within(t, 5*time.Second), 2)
	require.NoError(t, err)
	require.Len(t, ids, 2)
	assert.Contains(t, ids, elsewhere.ID())

	// The request is woken inside the scan that admitted its peer, before fill decides.
	// No tick can fire here, so anything seen is the forbidden lookup.
	time.Sleep(10 * tick)
	assert.Empty(t, routing.calls(), "routing was asked for a peer the peerstore already had")
}

func TestATickScansOnlyForARequestThatIsShort(t *testing.T) {
	stranger, client := newHost(t), newHost(t)
	// In the peerstore and never a member, so every read of it is a scan
	// reading it: the reap only ever looks at members.
	client.Peerstore().AddAddrs(stranger.ID(), stranger.Addrs(), time.Hour)

	h, store := spy(client)
	d := newDiscovery(t, h)

	require.Eventually(t, func() bool { return store.count(stranger.ID()) > 0 }, 5*time.Second, 10*time.Millisecond,
		"the opening scan never read the peerstore")
	opening := store.count(stranger.ID())

	// Twenty ticks with nobody waiting.
	time.Sleep(20 * tick)
	assert.Equal(t, opening, store.count(stranger.ID()), "the tick walked the whole peerstore for nobody")

	// And it walks it the moment somebody is short.
	_, err := d.Any(within(t, 10*tick))
	require.ErrorIs(t, err, discovery.ErrNoPeers)
	assert.Greater(t, store.count(stranger.ID()), opening, "a request that is short never got a scan")
}

func TestTheScanStopsAtTheCapRatherThanWalkingOn(t *testing.T) {
	one, two, client := newHost(t), newHost(t), newHost(t)
	remember(client, one, time.Hour)
	remember(client, two, time.Hour)

	h, store := spy(client)
	// No tick can fire during this, so the count below is the opening scan's
	// and nothing else's.
	d := newDiscovery(t, h, discovery.WithMaxPeers(1), discovery.WithRetryInterval(noTick))

	require.Eventually(t, func() bool { return len(d.Known()) == 1 }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(10 * tick) // long enough for a walk that did not stop to finish

	assert.Equal(t, 1, store.count(one.ID(), two.ID()),
		"the scan went on reading the peerstore after the set was full")
}

func TestOnlyOneLookupIsInFlightAtATime(t *testing.T) {
	client := newHost(t)
	routing := &fakeRouting{block: make(chan struct{})}

	d := newDiscovery(t, client, discovery.WithRouting(routing))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		_, err := d.Any(ctx)
		errs <- err
	}()

	require.Eventually(t, func() bool { return len(routing.calls()) == 1 }, 5*time.Second, 10*time.Millisecond)

	// Twenty ticks against a DHT that has not answered the first question.
	time.Sleep(20 * tick)
	assert.Len(t, routing.calls(), 1, "a lookup was issued while one was still in flight")

	// And the guard is a guard, not a stop: the answer arriving lets the next
	// one out.
	close(routing.block)
	require.Eventually(t, func() bool { return len(routing.calls()) > 1 }, 5*time.Second, 10*time.Millisecond,
		"no lookup was issued after the one in flight came back")

	cancel()
	require.ErrorIs(t, <-errs, discovery.ErrNoPeers)
}

func TestAPeerArrivingWakesAParkedRequest(t *testing.T) {
	server, client := newHost(t), newHost(t)
	serve(t, server)

	// No ticker to do it quietly, and a deadline far enough out that arriving
	// at it is unmistakable.
	d := newDiscovery(t, client, discovery.WithRetryInterval(noTick))

	type answer struct {
		pid peer.ID
		err error
	}
	answers := make(chan answer, 1)
	start := time.Now()
	go func() {
		pid, err := d.Any(within(t, 10*time.Second))
		answers <- answer{pid, err}
	}()

	time.Sleep(4 * tick)
	connectHosts(t, client, server)

	a := <-answers
	require.NoError(t, a.err)
	assert.Equal(t, server.ID(), a.pid)
	assert.Less(t, time.Since(start), 5*time.Second,
		"the request sat until its deadline beside a peer that had already arrived")
}
