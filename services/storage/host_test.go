package storage

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/services/cm"
	cmmock "github.com/taubyte/tau/services/cm/mock"
	"github.com/taubyte/tau/services/cm/settings"
)

// storageNode is the instance a storage node would run on: its peer, its databases, and
// the handles of the services it runs beside this one.
type storageNode struct {
	peer.Node
	t *testing.T

	mu      sync.Mutex
	running []service.Running
	started *server
	found   []libp2ppeer.ID
	config  func(into any) error
	cm      *cmmock.CM
}

func newStorageNode(t *testing.T) *storageNode {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)

	// THE REGION'S SETTINGS ARE CM'S, which a storage node waits for before it
	// starts.
	i := &storageNode{Node: p, t: t, cm: cmmock.New(settings.Defaults())}
	i.running = []service.Running{{Name: cm.Name, Service: i.cm}}
	return i
}

func (i *storageNode) Peer() peer.Node { return i.Node }

func (i *storageNode) Running() []service.Running {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := slices.Clone(i.running)
	if i.found != nil {
		out = append(out, service.Running{Service: finding(i.found)})
	}
	return out
}

func (i *storageNode) Config(into any) error {
	if i.config != nil {
		return i.config(into)
	}
	return nil
}

func (i *storageNode) Cloud() string { return "storage-test" }
func (*storageNode) Region() string  { return "" }
func (*storageNode) Now() time.Time  { return time.Now() }
func (i *storageNode) Root() string  { return i.t.TempDir() }

var _ service.Host = (*storageNode)(nil)

// finding is discovery naming the storage nodes it is given.
type finding []libp2ppeer.ID

func (f finding) Find(protocol.ID) discovery.Resolver { return discovery.Static(f...) }

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
	i.config = func(into any) error {
		into.(*Config).GraceGC = -time.Hour
		return nil
	}
	err = New().Start(t.Context(), i)
	require.ErrorContains(t, err, "must be positive")
}

func TestTwoStorageNodesHoldOneWrite(t *testing.T) {
	ctx := t.Context()
	a, b := newStorageNode(t), newStorageNode(t)
	a.found, b.found = []libp2ppeer.ID{b.ID()}, []libp2ppeer.ID{a.ID()}
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

func TestTheStoreIsReachedAsTheServiceBesideIt(t *testing.T) {
	ctx := t.Context()
	i := newStorageNode(t)
	s := i.start(t)
	i.mu.Lock()
	i.running = append(i.running, service.Running{Name: Descriptor.Name, Service: s})
	i.mu.Unlock()

	r := StoreOn(i)
	t.Cleanup(func() { r.Close() })
	at := assetsOf(t, "objects")
	v, err := r.Put(ctx, at, "o", Object{}, strings.NewReader("reached"))
	require.NoError(t, err)
	current, err := r.Current(ctx, at, "o")
	require.NoError(t, err)
	require.Equal(t, v.Stamp, current.Stamp)
	versions, err := r.Versions(ctx, at, "o", 0)
	require.NoError(t, err)
	require.Len(t, versions, 1)
	listed, err := r.List(ctx, at, "", "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"o"}, names(listed))
	body, err := r.Read(ctx, v.Record.RootCID)
	require.NoError(t, err)
	got, err := io.ReadAll(body)
	require.NoError(t, body.Close())
	require.NoError(t, err)
	require.Equal(t, "reached", string(got))
	_, err = r.Delete(ctx, at, "o")
	require.NoError(t, err)

	// NOWHERE TO REACH, and every call says so.
	none := StoreOn(newStorageNode(t))
	_, err = none.Put(ctx, at, "o", Object{}, strings.NewReader("x"))
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, err = none.Current(ctx, at, "o")
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, err = none.Versions(ctx, at, "o", 0)
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, err = none.List(ctx, at, "", "", 0)
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, err = none.Delete(ctx, at, "o")
	require.ErrorIs(t, err, service.ErrNoneRuns)
	_, err = none.Read(ctx, v.Record.RootCID)
	require.ErrorIs(t, err, service.ErrNoneRuns)
}

func TestStorageDoesNotStartWithoutTheRegionsSettings(t *testing.T) {
	i := newStorageNode(t)
	i.running = nil

	ctx, cancel := context.WithTimeout(t.Context(), 750*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, New().Start(ctx, i), context.DeadlineExceeded, "storage started with no cm to ask")
}
