package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
)

// fake is an instance with one peer, the handles it was told about and the nodes
// discovery names.
type fake struct {
	peer.Node
	locals map[string]any
	peers  []libp2ppeer.ID
}

func (f *fake) Name() string                           { return "caller" }
func (f *fake) Peer() peer.Node                        { return f.Node }
func (f *fake) Database(string) (kvdb.Instance, error) { return nil, errors.New("none") }
func (f *fake) Listen(protocol.ID, http.Handler) error { return nil }
func (f *fake) Handle(string, http.Handler) error      { return nil }
func (f *fake) Peers(string) []libp2ppeer.ID           { return f.peers }
func (f *fake) Local(name string) any                  { return f.locals[name] }
func (f *fake) Provide(any)                            {}
func (f *fake) Secrets() Vault                         { return nil }
func (f *fake) Root() string                           { return "" }
func (f *fake) Logger() log.StandardLogger             { return log.Logger("test") }
func (f *fake) Config(any) error                       { return nil }

var _ Host = (*fake)(nil)

type answer string

func TestTheServiceHereIsReachedWithNoClient(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &fake{Node: p, locals: map[string]any{"store": answer("here")}}

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
	h := &fake{Node: p, peers: []libp2ppeer.ID{p.ID(), "far"}}

	var at libp2ppeer.ID
	r := Reach(h, "store", func(_ host.Host, id libp2ppeer.ID) (answer, error) { at = id; return "far", nil })
	got, err := r.Now(t.Context())
	require.NoError(t, err)
	require.Equal(t, answer("far"), got)
	require.Equal(t, libp2ppeer.ID("far"), at, "never itself")

	// A HANDLE OF ANOTHER TYPE is not the service.
	h.locals = map[string]any{"store": 7}
	_, err = r.Now(t.Context())
	require.NoError(t, err)

	h.peers = []libp2ppeer.ID{p.ID()}
	nowhere := Reach(h, "store", func(host.Host, libp2ppeer.ID) (answer, error) { return "", nil })
	_, err = nowhere.Now(t.Context())
	require.ErrorIs(t, err, ErrNoneRuns)
}
