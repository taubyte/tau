package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer"
)

// fake is an instance with one peer, the handles it was told about and the nodes
// discovery names.
type fake struct {
	peer.Node
	running []Running
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

func TestTheServiceHereIsReachedWithNoClient(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &fake{Node: p, running: []Running{{Service: 7}, {Name: "store", Service: answer("here")}}}

	dialled := 0
	r := Reach(h, "store", func(host.Host, libp2ppeer.ID) (answer, error) { dialled++; return "far", nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("here"), got)
	require.Zero(t, dialled)
}

// holder runs as one thing and is reached as what it holds.
type holder struct{ held answer }

func TestAServiceHereIsReachedAsWhatItHolds(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &fake{Node: p, running: []Running{{Service: 7}, {Name: "store", Service: holder{held: "held"}}}}

	dialled := 0
	r := ReachAs(h, "store", func(s holder) answer { return s.held },
		func(host.Host, libp2ppeer.ID) (answer, error) { dialled++; return "far", nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("held"), got)
	require.Zero(t, dialled)

	h.running = []Running{{Name: "discovery", Service: directory{"far"}}}
	got, err = ReachAs(h, "store", func(s holder) answer { return s.held },
		func(host.Host, libp2ppeer.ID) (answer, error) { dialled++; return "far", nil }).Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("far"), got, "none here is a client of one elsewhere")
	require.Equal(t, 1, dialled)
}

func TestAServiceElsewhereIsDialledOnANodeDiscoveryNames(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &fake{Node: p, running: []Running{{Name: "discovery", Service: directory{p.ID(), "far"}}}}

	var at libp2ppeer.ID
	r := Reach(h, "store", func(_ host.Host, id libp2ppeer.ID) (answer, error) { at = id; return "far", nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("far"), got)
	require.Equal(t, libp2ppeer.ID("far"), at, "never itself")

	// A SERVICE OF ANOTHER TYPE is not the one asked for.
	h.running = []Running{{Service: 7}, {Name: "discovery", Service: directory{"far"}}}
	_, err = r.Now(t.Context())
	require.NoError(t, err)

	h.running = []Running{{Name: "discovery", Service: directory{p.ID()}}}
	nowhere := Reach(h, "store", func(host.Host, libp2ppeer.ID) (answer, error) { return "", nil })
	_, err = nowhere.Now(t.Context())
	require.ErrorIs(t, err, ErrNoneRuns)
}

func TestRoutedWorkGoesWhereTheDirectoryNamesFirst(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &fake{Node: p, running: []Running{{Name: "discovery", Service: directory{"far", p.ID()}}}}

	var at libp2ppeer.ID
	r := Routed(h, "clock", answer("here"), func(_ host.Host, id libp2ppeer.ID) (answer, error) { at = id; return "far", nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("far"), got, "the first named, even with this node among them")
	require.Equal(t, libp2ppeer.ID("far"), at)

	h.running = []Running{{Name: "discovery", Service: directory{p.ID(), "far"}}}
	got, err = r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("here"), got, "this node named first is this node, undialled")

	h.running = nil
	got, err = r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("here"), got, "nobody named is nobody but this node")
}

// directory is a discovery that names these nodes for every service.
type directory []libp2ppeer.ID

func (d directory) Peers(string) []libp2ppeer.ID { return d }

func TestNoDirectoryIsNobodyToAsk(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	r := Reach(&fake{Node: p}, "store", func(host.Host, libp2ppeer.ID) (answer, error) { return "", nil })
	_, err := r.Now(t.Context())
	require.ErrorIs(t, err, ErrNoneRuns)
	require.ErrorContains(t, err, "no directory")
}

// recorder is a service that keeps the host it started with.
type recorder struct {
	host Host
	err  error
}

func (r *recorder) Start(_ context.Context, h Host) error { r.host = h; return r.err }
func (r *recorder) Close() error                          { return nil }

func TestALonePeerRunsServicesBesideEachOther(t *testing.T) {
	p := peer.Mock(t.Context())
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
