package discovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	discoveryv2 "github.com/taubyte/tau/pkg/proto/gen/tau/discovery/v2"
	"github.com/taubyte/tau/pkg/stats"
)

// Every dependency here is a fake: a mocknet host for identity, an in-memory
// bus, and a function for this node's own numbers. Nothing binds a port and
// nothing needs a universe — the point of pushing every rule down into
// pkg/stats is that this file only has to prove the plumbing.

func newHost(t testing.TB) host.Host {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h, err := mn.GenPeer()
	require.NoError(t, err)
	return h
}

// memBus is a bus every subscriber on it shares. Publish fans out to everyone
// currently subscribed, including the publisher — a node must be correct on a
// bus that echoes and on one that does not, and this one echoes so the
// "observe my own report directly" path is exercised against the harder case.
type memBus struct {
	mu     sync.Mutex
	subs   []chan []byte
	sent   [][]byte
	failOn error
}

func (b *memBus) Publish(_ context.Context, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failOn != nil {
		return b.failOn
	}
	b.sent = append(b.sent, data)
	for _, c := range b.subs {
		select {
		case c <- data:
		default: // a slow subscriber does not block a broadcast
		}
	}
	return nil
}

func (b *memBus) Subscribe(ctx context.Context) (<-chan []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := make(chan []byte, 64)
	b.subs = append(b.subs, c)
	return c, nil
}

func (b *memBus) published() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]byte(nil), b.sent...)
}

// deliver pushes an arbitrary payload to every subscriber, so a test can play
// another node — or a malformed message.
func (b *memBus) deliver(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.subs {
		c <- data
	}
}

func fixedSnapshot(s Snapshot) Reporter {
	return ReporterFunc(func(context.Context) (Snapshot, error) { return s, nil })
}

func newService(t *testing.T, bus Bus, r Reporter, adjust func(*Config)) *server {
	t.Helper()
	cfg := Config{Interval: 20 * time.Millisecond}
	if adjust != nil {
		adjust(&cfg)
	}
	s, err := build(newHost(t), cfg, withBus(bus), withReporter(r))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// eventually is a small poll so a test asserts on a goroutine's effect without
// sleeping for a fixed guess.
func eventually(t *testing.T, why string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting: %s", why)
}

// TestANodeIsRoutableBeforeTheFirstIntervalElapses. A node that waited a full
// tick before saying anything would be invisible for that long on every boot,
// which is the window a deploy most needs it to be visible in.
func TestANodeIsRoutableBeforeTheFirstIntervalElapses(t *testing.T) {
	bus := &memBus{}
	s := newService(t, bus, fixedSnapshot(Snapshot{Load: 0.25, Ready: true, Serving: []string{"shop"}}), func(c *Config) {
		c.Interval = time.Hour // the first report cannot be a tick
	})
	require.NoError(t, s.serve(context.Background()))

	eventually(t, "the first report to be sent immediately", func() bool {
		return len(bus.published()) == 1
	})
	require.Len(t, s.Route("shop", time.Minute), 1,
		"and it is in its own routing answer without waiting to hear itself back")
	require.Equal(t, []string{s.Peer()}, s.RoutePeers("shop", time.Minute, nil), "and by peer alone")
}

func TestAViewNotYetBuiltRoutesNobody(t *testing.T) {
	s := newService(t, &memBus{}, fixedSnapshot(Snapshot{Ready: true, Serving: []string{"shop"}}), nil)
	require.Empty(t, s.Route("shop", time.Minute))
	held := []string{"kept"}
	require.Equal(t, held, s.RoutePeers("shop", time.Minute, held), "what the caller held is handed back")
}

// TestThisNodeSeesItselfWithoutWaitingForTheBusToEchoIt. Whether a bus delivers
// to its own publisher is a property of the bus; a node that depended on the
// echo would be missing from its own answers on any bus that does not.
func TestThisNodeSeesItselfWithoutWaitingForTheBusToEchoIt(t *testing.T) {
	// A bus that never echoes: Publish records, and fans out to nobody.
	silent := &noEchoBus{}
	s := newService(t, silent, fixedSnapshot(Snapshot{Load: 0.5, Ready: true, Serving: []string{"shop"}}), nil)
	require.NoError(t, s.serve(context.Background()))

	eventually(t, "this node to appear in its own view", func() bool {
		return len(s.Route("shop", time.Minute)) == 1
	})
	got, ok := s.Get(s.Peer())
	require.True(t, ok)
	require.Equal(t, 0.5, got.Load)
}

type noEchoBus struct{ memBus }

func (b *noEchoBus) Publish(_ context.Context, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, data)
	return nil
}

// TestAnotherNodesReportIsFoldedIn — the listening half.
func TestAnotherNodesReportIsFoldedIn(t *testing.T) {
	bus := &memBus{}
	s := newService(t, bus, fixedSnapshot(Snapshot{Load: 0.9, Ready: true, Serving: []string{"shop"}}), nil)
	require.NoError(t, s.serve(context.Background()))

	b, err := proto.Marshal(&discoveryv2.Report{
		Peer: "other", Incarnation: 1, Load: 0.1, Ready: true, Serving: []string{"shop"},
	})
	require.NoError(t, err)
	bus.deliver(b)

	// This node's own report is folded in by its first broadcast, which
	// runs beside the listener: either may land first.
	eventually(t, "the other node to be heard beside this one", func() bool {
		_, other := s.Get("other")
		_, self := s.Get(s.Peer())
		return other && self
	})

	got := s.Route("shop", time.Minute)
	require.Len(t, got, 2)
	require.Equal(t, "other", got[0].Peer, "and it sorts ahead of this saturated node")
}

// TestAMalformedReportDoesNotDeafenTheNode. The bus is a broadcast medium: one
// bad message must not stop a node hearing every subsequent one.
func TestAMalformedReportDoesNotDeafenTheNode(t *testing.T) {
	bus := &memBus{}
	s := newService(t, bus, fixedSnapshot(Snapshot{Ready: true}), nil)
	require.NoError(t, s.serve(context.Background()))

	bus.deliver([]byte("this is not a protobuf at all, not even close"))

	good, err := proto.Marshal(&discoveryv2.Report{Peer: "other", Ready: true, Serving: []string{"shop"}})
	require.NoError(t, err)
	bus.deliver(good)

	eventually(t, "the node to still be listening after garbage", func() bool {
		_, ok := s.Get("other")
		return ok
	})
}

// TestAReportThisNodeCannotMakeIsNotSent. A Reporter that fails, or that
// answers with a load outside 0..1, produces silence rather than a lie —
// readers can see the row aging, which is the honest signal.
func TestAReportThisNodeCannotMakeIsNotSent(t *testing.T) {
	type testCase struct {
		name string
		r    Reporter
	}
	for _, tc := range []testCase{
		{"the reporter failed", ReporterFunc(func(context.Context) (Snapshot, error) {
			return Snapshot{}, errors.New("cannot measure myself")
		})},
		{"the reporter answered out of range", fixedSnapshot(Snapshot{Load: 1.4, Ready: true})},
		{"and below range", fixedSnapshot(Snapshot{Load: -1, Ready: true})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := &memBus{}
			s := newService(t, bus, tc.r, nil)
			require.NoError(t, s.serve(context.Background()))

			time.Sleep(80 * time.Millisecond) // several intervals
			require.Empty(t, bus.published(), "silence is the honest answer")
			require.Empty(t, s.All(), "and this node does not fold a number it refused to send")
		})
	}
}

// TestAFailingBusIsNotFatal. The next tick will try again.
func TestAFailingBusIsNotFatal(t *testing.T) {
	bus := &memBus{failOn: errors.New("no peers")}
	s := newService(t, bus, fixedSnapshot(Snapshot{Load: 0.1, Ready: true, Serving: []string{"shop"}}), nil)
	require.NoError(t, s.serve(context.Background()))

	eventually(t, "this node to know its own numbers even though nothing left the box", func() bool {
		return len(s.Route("shop", time.Minute)) == 1
	})

	bus.mu.Lock()
	bus.failOn = nil
	bus.mu.Unlock()

	eventually(t, "the next tick to get through", func() bool { return len(bus.published()) > 0 })
}

// TestSubscribeFailureIsAStartFailure. Not being able to hear anybody is not a
// degraded mode — a router that hears nothing routes everything to itself.
func TestSubscribeFailureIsAStartFailure(t *testing.T) {
	s := newService(t, &deafBus{}, fixedSnapshot(Snapshot{Ready: true}), nil)
	err := s.serve(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "subscribing")

	// And it can be started again once the bus works, rather than being stuck.
	require.NoError(t, s.Close())
}

type deafBus struct{ memBus }

func (b *deafBus) Subscribe(context.Context) (<-chan []byte, error) {
	return nil, errors.New("no topic")
}

func TestStartTwiceIsRefusedAndCloseIsIdempotent(t *testing.T) {
	bus := &memBus{}
	s := newService(t, bus, fixedSnapshot(Snapshot{Ready: true}), nil)

	require.NoError(t, s.serve(context.Background()))
	require.Error(t, s.serve(context.Background()))

	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
	require.Error(t, s.serve(context.Background()), "a closed service does not come back")
}

func TestCloseBeforeStartIsFine(t *testing.T) {
	s := newService(t, &memBus{}, fixedSnapshot(Snapshot{Ready: true}), nil)
	require.NoError(t, s.Close())
}

// TestAClosedSubscriptionEndsTheListener rather than spinning on a closed
// channel.
func TestAClosedSubscriptionEndsTheListener(t *testing.T) {
	bus := &closingBus{ch: make(chan []byte, 1)}
	s := newService(t, bus, fixedSnapshot(Snapshot{Ready: true}), nil)
	require.NoError(t, s.serve(context.Background()))

	close(bus.ch)
	require.NoError(t, s.Close())
}

type closingBus struct {
	memBus
	ch chan []byte
}

func (b *closingBus) Subscribe(context.Context) (<-chan []byte, error) { return b.ch, nil }

// TestAnIncarnationIsChosenPerRun, so a restarted node's reports are not
// overwritten by ones still in flight from the run before.
func TestAnIncarnationIsChosenPerRun(t *testing.T) {
	bus := &memBus{}
	old := newService(t, bus, fixedSnapshot(Snapshot{Load: 0.9, Ready: true, Serving: []string{"shop"}}), nil)
	require.NotZero(t, old.incarnation, "it comes from the clock")

	at := time.Unix(1_700_000_000, 0)
	pinned, err := build(newHost(t), Config{Interval: time.Second},
		withBus(bus), withReporter(fixedSnapshot(Snapshot{})), withClock(func() time.Time { return at }))
	require.NoError(t, err)
	t.Cleanup(func() { pinned.Close() })
	require.Equal(t, uint64(at.UnixNano()), pinned.incarnation,
		"and an injected clock decides it, which is how a test says 'the run after that one'")
}

func TestConfigRefusesWhatItCannotWorkWithout(t *testing.T) {
	h := newHost(t)

	_, err := build(h, Config{}, withReporter(fixedSnapshot(Snapshot{})))
	require.ErrorIs(t, err, ErrNoBus)

	_, err = build(h, Config{}, withBus(&memBus{}))
	require.ErrorIs(t, err, ErrNoReporter)

	_, err = build(h, Config{}, withBus(nil))
	require.ErrorIs(t, err, ErrNoBus, "an option refuses its own bad argument")

	_, err = build(h, Config{}, withReporter(nil))
	require.ErrorIs(t, err, ErrNoReporter)

	_, err = build(h, Config{}, withBus(&memBus{}), withReporter(fixedSnapshot(Snapshot{})), withClock(nil))
	require.Error(t, err)

	_, err = build(nil, Config{}, withBus(&memBus{}), withReporter(fixedSnapshot(Snapshot{})))
	require.Error(t, err, "a service with no host does not know who it is")

	_, err = build(h, Config{Interval: -time.Second}, withBus(&memBus{}), withReporter(fixedSnapshot(Snapshot{})))
	require.Error(t, err)

	// Fresh shorter than the interval would make every row stale before its
	// replacement was even sent.
	_, err = build(h, Config{Interval: time.Minute, Fresh: time.Second},
		withBus(&memBus{}), withReporter(fixedSnapshot(Snapshot{})))
	require.Error(t, err)
}

// TestTheFreshnessWindowIsAMultipleOfTheInterval so two lost reports do not
// make a healthy node vanish.
func TestTheFreshnessWindowIsAMultipleOfTheInterval(t *testing.T) {
	h := newHost(t)
	s, err := build(h, Config{Interval: time.Second}, withBus(&memBus{}), withReporter(fixedSnapshot(Snapshot{})))
	require.NoError(t, err)
	require.Equal(t, freshIntervals*time.Second, s.cfg.Fresh)
	require.Equal(t, forgetIntervals*time.Second, s.cfg.Forget)
}

func TestFreshAndAllReadThrough(t *testing.T) {
	bus := &memBus{}
	s := newService(t, bus, fixedSnapshot(Snapshot{Load: 0.3, Ready: true, Serving: []string{"shop"}}), nil)
	require.NoError(t, s.serve(context.Background()))

	eventually(t, "this node to report", func() bool { return len(s.All()) == 1 })
	require.Len(t, s.Fresh(), 1)
	require.Equal(t, s.Peer(), s.All()[0].Peer)
}

// TestAddressesReachTheWireAndComeBack. DNS is made of these, so a field that
// was dropped in encoding would produce a routing answer with nowhere to send
// anyone — and it would look like a working answer.
func TestAddressesReachTheWireAndComeBack(t *testing.T) {
	bus := &memBus{}
	s := newService(t, bus, fixedSnapshot(Snapshot{
		Load: 0.2, Ready: true, Serving: []string{"shop"},
		Addresses: []string{"203.0.113.7", "2001:db8::1"},
	}), nil)
	require.NoError(t, s.serve(context.Background()))

	eventually(t, "a report to be published", func() bool { return len(bus.published()) > 0 })

	var decoded discoveryv2.Report
	require.NoError(t, proto.Unmarshal(bus.published()[0], &decoded))
	require.Equal(t, []string{"203.0.113.7", "2001:db8::1"}, decoded.GetAddresses())

	got := s.Route("shop", time.Minute)
	require.Len(t, got, 1)
	require.Equal(t, []string{"203.0.113.7", "2001:db8::1"}, got[0].Addresses)
}

// TestSOCKETSREACHTHEWIREFROMBOTHDIRECTIONS. The port a client dials lives
// nowhere else — it is not authored, not derivable and gone when the node
// restarts — so a field dropped in either direction is a name that resolves to
// an address and a port nobody is listening on.
//
// The DECODE half is the one that was broken: a node folded its own report in
// directly and lost every other node's, so a single-node test passed and a
// fleet answered SRV for one node only.
func TestSocketsReachTheWireAndComeBack(t *testing.T) {
	bus := &memBus{}
	mine := stats.Socket{Host: "db.example.com", Protocol: "postgres", Transport: "tcp", Port: 41337}
	s := newService(t, bus, fixedSnapshot(Snapshot{
		Load: 0.9, Ready: true, Serving: []string{"db.example.com"},
		Sockets: []stats.Socket{mine},
	}), nil)
	require.NoError(t, s.serve(context.Background()))

	eventually(t, "a report to be published", func() bool { return len(bus.published()) > 0 })

	var decoded discoveryv2.Report
	require.NoError(t, proto.Unmarshal(bus.published()[0], &decoded))
	require.Len(t, decoded.GetSockets(), 1)
	require.Equal(t, uint32(41337), decoded.GetSockets()[0].GetPort())
	require.Equal(t, "postgres", decoded.GetSockets()[0].GetProtocol())

	theirs := &discoveryv2.Socket{Host: "db.example.com", Protocol: "postgres", Transport: "tcp", Port: 5001}
	b, err := proto.Marshal(&discoveryv2.Report{
		Peer: "other", Incarnation: 1, Load: 0.1, Ready: true,
		Serving: []string{"db.example.com"}, Sockets: []*discoveryv2.Socket{theirs},
	})
	require.NoError(t, err)
	bus.deliver(b)

	eventually(t, "the other node to be heard", func() bool {
		n, ok := s.Get("other")
		return ok && len(n.Sockets) == 1
	})

	got := s.Route("db.example.com", time.Minute)
	require.Len(t, got, 2)
	require.Equal(t, uint16(5001), got[0].Sockets[0].Port, "the other node's own port, not this one's")
	require.Equal(t, []stats.Socket{mine}, got[1].Sockets)
}

// TestAPORTTHATCANNOTBEAPORT is dropped rather than truncated: the wire carries
// uint32 because proto3 has no uint16, and 70000 silently became 4464 — a
// routable-looking answer to a listener nobody has.
func TestASocketWithAnImpossiblePortIsDropped(t *testing.T) {
	bus := &memBus{}
	s := newService(t, bus, fixedSnapshot(Snapshot{Ready: true, Serving: []string{"db.example.com"}}), nil)
	require.NoError(t, s.serve(context.Background()))

	b, err := proto.Marshal(&discoveryv2.Report{
		Peer: "other", Incarnation: 1, Ready: true, Serving: []string{"db.example.com"},
		Sockets: []*discoveryv2.Socket{
			{Host: "db.example.com", Protocol: "postgres", Transport: "tcp", Port: 70000},
			{Host: "db.example.com", Protocol: "redis", Transport: "tcp", Port: 0},
			{Host: "db.example.com", Protocol: "mysql", Transport: "tcp", Port: 3306},
		},
	})
	require.NoError(t, err)
	bus.deliver(b)

	eventually(t, "the other node to be heard", func() bool {
		_, ok := s.Get("other")
		return ok
	})

	n, _ := s.Get("other")
	require.Len(t, n.Sockets, 1, "the impossible one and the absent one are both gone")
	require.Equal(t, "mysql", n.Sockets[0].Protocol)
}

func TestDecodeRefusesGarbage(t *testing.T) {
	_, err := Decode([]byte{0xff, 0xff, 0xff})
	require.Error(t, err)
}
