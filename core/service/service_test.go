package service

import (
	"testing"

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

// directory is a discovery that names these nodes for every service.
type directory []libp2ppeer.ID

func (d directory) Peers(string) []libp2ppeer.ID { return d }

func TestNoDirectoryIsNobodyToAsk(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	r := Reach(&fake{Node: p}, "store", func(host.Host, libp2ppeer.ID) (answer, error) { return "", nil })
	_, err := r.Now(t.Context())
	require.ErrorIs(t, err, ErrNoneRuns)
}
