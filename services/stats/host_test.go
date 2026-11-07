package stats

import (
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/stats"
)

// addrsOnly is a host that only answers what it is listening on. addresses asks
// nothing else of it, and a full fake would be nine methods nothing calls.
type addrsOnly struct {
	host.Host
	addrs []ma.Multiaddr
}

func (a addrsOnly) Addrs() []ma.Multiaddr { return a.addrs }

func listeningOn(t *testing.T, on ...string) addrsOnly {
	t.Helper()
	out := make([]ma.Multiaddr, 0, len(on))
	for _, s := range on {
		a, err := ma.NewMultiaddr(s)
		require.NoError(t, err)
		out = append(out, a)
	}
	return addrsOnly{addrs: out}
}

func TestWhatANodePublishesAsItsAddress(t *testing.T) {
	require.Equal(t, []string{"203.0.113.9"}, addresses(listeningOn(t,
		// Not an address with an IP in it: nothing a client can be handed.
		"/unix/tmp/tau.sock",
		// "Every address" is not an address. A client handed 0.0.0.0 has been
		// told nothing.
		"/ip4/0.0.0.0/tcp/4001",
		"/ip6/::/tcp/4001",
		// The same IP on two transports is one address.
		"/ip4/203.0.113.9/tcp/4001",
		"/ip4/203.0.113.9/udp/4001/quic-v1",
		// Present, and outranked.
		"/ip4/127.0.0.1/tcp/4001",
	)))
}

func TestLoopbackIsAnAnswerOnlyWhenThereIsNoOther(t *testing.T) {
	require.Equal(t, []string{"127.0.0.1", "::1"}, addresses(listeningOn(t, "/ip4/127.0.0.1/tcp/4001", "/ip6/::1/tcp/4001")))
	require.Empty(t, addresses(listeningOn(t, "/ip4/0.0.0.0/tcp/4001")), "every address is no address")
}

// instance is a node's instance as stats sees it: its peer, what runs on it, its
// cloud and its section.
type instance struct {
	peer.Node
	running []service.Running
	config  func(any) error
}

func (i *instance) Peer() peer.Node            { return i.Node }
func (i *instance) Running() []service.Running { return i.running }
func (i *instance) Root() string               { return "" }
func (i *instance) Cloud() string              { return "test" }
func (*instance) Region() string               { return "" }
func (*instance) Now() time.Time               { return time.Now() }
func (i *instance) Config(into any) error {
	if i.config != nil {
		return i.config(into)
	}
	return nil
}

// sockets is a service that adds its part to the report.
type sockets struct{}

func (sockets) Report(s *Snapshot) {
	s.Sockets = append(s.Sockets, stats.Socket{Host: "db.example", Port: 5432})
	s.Serving = append(s.Serving, "db.example", "worker")
}

func TestStatsReportsWhatItsInstanceRuns(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	d := New()

	// BEFORE IT STARTS IT KNOWS NOBODY, and says so rather than failing.
	require.Nil(t, d.Routes("worker"))
	require.Nil(t, d.Fresh())
	require.Nil(t, d.All())
	require.Nil(t, d.Placement())
	require.Zero(t, d.Pending(time.Minute))
	_, ok := d.Get(p.ID().String())
	require.False(t, ok)

	h := &instance{Node: p, running: []service.Running{
		{Name: "worker", Service: sockets{}},
		{Name: Descriptor.Name, Service: d},
		{Service: "not a service"},
	}}
	require.NoError(t, d.Start(t.Context(), h))
	t.Cleanup(func() { d.Close() })

	var row stats.Node
	require.Eventually(t, func() bool {
		row, ok = d.Get(p.ID().String())
		return ok
	}, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"db.example", Descriptor.Name, "worker"}, row.Serving, "sorted, once each")
	require.Equal(t, []stats.Socket{{Host: "db.example", Port: 5432}}, row.Sockets)
	require.Equal(t, p.ID().String(), d.Peer())
}

func TestStatsRefusesWhatItCannotStartWith(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &instance{Node: p, config: func(any) error { return errors.New("no such key") }}
	require.ErrorContains(t, New().Start(t.Context(), h), "no such key")

	h.config = func(into any) error {
		into.(*Config).Interval = -time.Second
		return nil
	}
	require.Error(t, New().Start(t.Context(), h))
}
