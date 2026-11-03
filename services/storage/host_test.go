package storage

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/placement"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/register"
)

// storageNode is the instance a storage node would run on: its peer, its databases, and
// the handles of the services it runs beside this one.
type storageNode struct {
	peer.Node
	t *testing.T

	mu       sync.Mutex
	running  []any
	started  *server
	members  func() []placement.Member
	config   func(into any) error
	refusing error
}

func newStorageNode(t *testing.T) *storageNode {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)

	// The fleet document is the register's; an unpublished one is the defaults,
	// which a write needs to have read.
	reg, err := register.New(p.Host(), register.WithStore(pkgregister.NewMemStore()))
	require.NoError(t, err)
	t.Cleanup(func() { reg.Close() })

	return &storageNode{Node: p, t: t, running: []any{reg.Local()}}
}

func (i *storageNode) Name() string    { return Descriptor.Name }
func (i *storageNode) Peer() peer.Node { return i.Node }

func (i *storageNode) Database(name string) (kvdb.Instance, error) {
	if i.refusing != nil {
		return nil, i.refusing
	}
	db, err := pkgkvdb.Open(i.Node, Descriptor.Name+"/"+name)
	if err != nil {
		return nil, err
	}
	i.t.Cleanup(db.Close)
	return db, nil
}

func (i *storageNode) Peers(string) []libp2ppeer.ID { return nil }

func (i *storageNode) Running() []any {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := slices.Clone(i.running)
	if i.members != nil {
		out = append(out, placed(i.members))
	}
	return out
}

func (i *storageNode) Logger() log.StandardLogger { return log.Logger("storage-test") }

func (i *storageNode) Config(into any) error {
	if i.config != nil {
		return i.config(into)
	}
	return nil
}

func (*storageNode) Listen(protocol.ID, http.Handler) error { return nil }
func (*storageNode) Handle(string, http.Handler) error      { return nil }
func (*storageNode) Secrets() service.Vault                 { return nil }
func (i *storageNode) Root() string                         { return i.t.TempDir() }

var _ service.Host = (*storageNode)(nil)

// placed is a discovery that places on the members it is given.
type placed func() []placement.Member

func (p placed) PlacementOf(string) []placement.Member { return p() }

// start runs storage on this instance, as the instance would.
func (i *storageNode) start(t *testing.T) *server {
	t.Helper()
	s := New().(*server)
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), i))
	i.started = s
	return s
}

func (i *storageNode) service() *server { return i.started }

func TestStorageRunsFromWhatItsInstanceGivesIt(t *testing.T) {
	ctx := t.Context()
	i := newStorageNode(t)

	s := i.start(t)

	at := assetsOf(t, "logs")
	v, err := s.Put(ctx, at, "job-1", Object{ContentType: "text/plain", Metadata: map[string]string{"encoding": "lzw"}}, strings.NewReader("a log"))
	require.NoError(t, err)

	got, err := s.Current(ctx, at, "job-1")
	require.NoError(t, err)
	require.Equal(t, v.Record.RootCID, got.Record.RootCID)
	require.Equal(t, map[string]string{"encoding": "lzw"}, got.Record.Metadata)

	r, err := s.Read(ctx, v.Record.RootCID)
	require.NoError(t, err)
	body, err := io.ReadAll(r)
	require.NoError(t, r.Close())
	require.NoError(t, err)
	require.Equal(t, "a log", string(body))

	buckets, err := s.catalog.Buckets(ctx)
	require.NoError(t, err)
	require.Equal(t, []Address{at}, buckets, "the storageNode it wrote is catalogued")
}

func TestStorageRefusesWhatItCannotStartWith(t *testing.T) {
	i := newStorageNode(t)
	i.config = func(any) error { return errors.New("no such key") }
	err := New().Start(t.Context(), i)
	require.ErrorContains(t, err, "no such key")

	i = newStorageNode(t)
	i.refusing = errors.New("disk full")
	err = New().Start(t.Context(), i)
	require.ErrorContains(t, err, "disk full")

	i = newStorageNode(t)
	i.config = func(into any) error {
		into.(*Config).Stabilize, into.(*Config).Liveness = 1, 2
		return nil
	}
	err = New().Start(t.Context(), i)
	require.ErrorContains(t, err, "stabilize")
}

func TestTwoStorageNodesHoldOneWrite(t *testing.T) {
	ctx := t.Context()
	a, b := newStorageNode(t), newStorageNode(t)
	members := func() []placement.Member {
		return []placement.Member{{ID: a.ID().String(), Accepts: true}, {ID: b.ID().String(), Accepts: true}}
	}
	a.members, b.members = members, members
	require.NoError(t, peer.LinkAllPeers())
	require.NoError(t, a.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: b.ID(), Addrs: b.Host().Addrs()}))

	for _, i := range []*storageNode{a, b} {
		i.start(t)
	}

	// TWO NODES ARE BOTH THE HOLDERS AND THE BARRIER: the write returns only once
	// b fetched the blocks from a over bitswap and appended the row.
	at := assetsOf(t, "QmPackage")
	content := strings.Repeat("an artifact larger than one block ", 20000)
	v, err := a.service().Put(ctx, at, "job-1", Object{}, strings.NewReader(content))
	require.NoError(t, err)

	root, err := cid.Decode(v.Record.RootCID)
	require.NoError(t, err)
	held, err := b.service().bytes.Has(ctx, root)
	require.NoError(t, err)
	require.True(t, held, "b holds the bytes it was asked to")

	r, err := b.service().Read(ctx, v.Record.RootCID)
	require.NoError(t, err)
	body, err := io.ReadAll(r)
	require.NoError(t, r.Close())
	require.NoError(t, err)
	require.Equal(t, content, string(body))

	got, err := b.service().Current(ctx, at, "job-1")
	require.NoError(t, err)
	require.Equal(t, v.Stamp, got.Stamp)
}

func TestAPeersBlocksAreReadOnlyWhereTheyAre(t *testing.T) {
	ctx := t.Context()
	i := newStorageNode(t)
	bytes := peerBytes{Node: i.Node}

	content := strings.Repeat("x", 600<<10)
	root, size, blocks, err := bytes.Add(ctx, strings.NewReader(content))
	require.NoError(t, err)
	require.Equal(t, uint64(len(content)), size)
	require.Greater(t, len(blocks), 1, "a file larger than a chunk is a tree")

	links, err := bytes.Links(ctx, root)
	require.NoError(t, err)
	require.NotEmpty(t, links)
	leaf, err := bytes.Links(ctx, links[0])
	require.NoError(t, err)
	require.Empty(t, leaf, "a raw leaf links nothing")

	require.NoError(t, bytes.Delete(ctx, links[0]))
	held, err := bytes.Has(ctx, links[0])
	require.NoError(t, err)
	require.False(t, held)
	_, err = bytes.Links(ctx, links[0])
	require.Error(t, err, "a block not here is an error, never a fetch")

	_, err = bytes.Fetch(ctx, root, "not-a-peer")
	require.Error(t, err)
}
