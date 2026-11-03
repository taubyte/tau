package storage

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ipfs/go-cid"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mh "github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
)

func names(vs []Version) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

func TestAListIsEachLiveNameOnceInNameOrder(t *testing.T) {
	ctx := t.Context()
	at := assetsOf(t, "objects")
	c := newCluster(t, 4, fixedCatalog{at})
	put := c.client(t, 0, 0)

	for _, name := range []string{"a/2", "a/1", "b", "ab"} {
		_, err := put.Put(ctx, at, name, Object{}, strings.NewReader(name))
		require.NoError(t, err)
	}
	newest, err := put.Put(ctx, at, "a/1", Object{Metadata: map[string]string{"k": "v"}}, strings.NewReader("again"))
	require.NoError(t, err)
	_, err = put.Delete(ctx, at, "b")
	require.NoError(t, err)

	// FROM EVERY NODE, a holder of the shard or not, and over the wire.
	for i := range c.nodes {
		all, err := c.client(t, i, i).List(ctx, at, "", "", 0)
		require.NoError(t, err, "node %d", i)
		require.Equal(t, []string{"a/1", "a/2", "ab"}, names(all), "node %d: a deleted name is not listed", i)
		require.Equal(t, newest.Stamp, all[0].Stamp, "a name's current version is its newest")
		require.Equal(t, map[string]string{"k": "v"}, all[0].Record.Metadata)
		require.NotZero(t, all[0].Record.CreatedMS)
	}

	under, err := c.client(t, 1, 1).List(ctx, at, "a", "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"a/1", "a/2", "ab"}, names(under), "a prefix of the name's bytes, not a path")

	under, err = c.client(t, 1, 1).List(ctx, at, "a/", "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"a/1", "a/2"}, names(under))

	after, err := c.client(t, 2, 2).List(ctx, at, "", "a/1", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"a/2", "ab"}, names(after), "resumed past every version of a/1")

	one, err := c.client(t, 3, 3).List(ctx, at, "", "", 1)
	require.NoError(t, err)
	require.Equal(t, []string{"a/1"}, names(one))

	_, err = c.client(t, 0, 0).List(ctx, Address{}, "", "", 0)
	require.Error(t, err, "an address that is no address")
}

func TestABlockIsReadFromWhoeverHoldsIt(t *testing.T) {
	ctx := t.Context()
	at := assetsOf(t, "objects")
	c := newCluster(t, 5, fixedCatalog{at})

	v, err := c.client(t, 0, 0).Put(ctx, at, "build-1", Object{}, strings.NewReader("the bytes"))
	require.NoError(t, err)

	for i := range c.nodes {
		r, err := c.client(t, i, i).Read(ctx, v.Record.RootCID)
		require.NoError(t, err, "node %d", i)
		body, err := io.ReadAll(r)
		require.NoError(t, r.Close())
		require.NoError(t, err)
		require.Equal(t, "the bytes", string(body), "node %d", i)
	}

	sum, err := mh.Sum([]byte("never stored"), mh.SHA2_256, -1)
	require.NoError(t, err)
	missing := cid.NewCidV1(cid.Raw, sum).String()
	_, err = c.client(t, 1, 1).Read(ctx, missing)
	require.ErrorIs(t, err, ErrNotFound, "no holder has it")

	_, err = c.nodes[0].Read(ctx, "not a cid")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = c.client(t, 0, 0).Read(ctx, "not a cid")
	require.Error(t, err)
}

func TestAReachAsksTheServiceHereThenAnyStorageNode(t *testing.T) {
	ctx := t.Context()
	at := assetsOf(t, "objects")
	c := newCluster(t, 3, fixedCatalog{at})

	// NOT A STORAGE NODE: the first id answers nothing, so the next one is asked
	// before any of the content was read.
	unreachable := libp2ppeer.ID("not-a-storage-node")
	remote := NewReach(c.caller, func() (Store, bool) { return nil, false },
		func() []libp2ppeer.ID { return append([]libp2ppeer.ID{unreachable}, c.ids...) })
	t.Cleanup(func() { remote.Close() })

	v, err := remote.Put(ctx, at, "o", Object{ContentType: "text/plain"}, strings.NewReader("over the wire"))
	require.NoError(t, err)
	got, err := remote.Current(ctx, at, "o")
	require.NoError(t, err)
	require.Equal(t, v.Stamp, got.Stamp)
	vs, err := remote.Versions(ctx, at, "o", 0)
	require.NoError(t, err)
	require.Len(t, vs, 1)
	listed, err := remote.List(ctx, at, "", "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"o"}, names(listed))
	r, err := remote.Read(ctx, v.Record.RootCID)
	require.NoError(t, err)
	body, err := io.ReadAll(r)
	require.NoError(t, r.Close())
	require.NoError(t, err)
	require.Equal(t, "over the wire", string(body))
	_, err = remote.Delete(ctx, at, "o")
	require.NoError(t, err)
	_, err = remote.Current(ctx, at, "o")
	require.ErrorIs(t, err, ErrNotFound, "an answer is the answer, not a reason to ask the next node")

	// THE SERVICE ON THIS INSTANCE is asked without a hop, and no other.
	local := NewReach(c.caller, func() (Store, bool) { return c.nodes[0], true }, func() []libp2ppeer.ID { return nil })
	t.Cleanup(func() { local.Close() })
	here, err := local.Put(ctx, at, "here", Object{}, strings.NewReader("in process"))
	require.NoError(t, err)
	vs, err = local.Versions(ctx, at, "here", 0)
	require.NoError(t, err)
	require.Len(t, vs, 1)
	listed, err = local.List(ctx, at, "h", "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"here"}, names(listed))
	r, err = local.Read(ctx, here.Record.RootCID)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	_, err = local.Delete(ctx, at, "here")
	require.NoError(t, err)

	// A PUT THAT CONSUMED ITS STREAM is not offered to another node.
	failing := NewReach(c.caller, func() (Store, bool) { return nil, false }, func() []libp2ppeer.ID { return c.ids })
	t.Cleanup(func() { failing.Close() })
	_, err = failing.Put(ctx, at, "broken", Object{}, io.MultiReader(strings.NewReader("some"), erroring{}))
	require.Error(t, err)

	nobody := NewReach(c.caller, func() (Store, bool) { return nil, false }, func() []libp2ppeer.ID { return nil })
	_, err = nobody.Current(ctx, at, "o")
	require.Error(t, err, "no storage node is known")
}

type erroring struct{}

func (erroring) Read([]byte) (int, error) { return 0, errors.New("the source went away") }

func TestTheCatalogIsEveryInstanceWrittenOnce(t *testing.T) {
	ctx := t.Context()
	db, err := kvdbmock.New().New(nil, "catalog", 0)
	require.NoError(t, err)
	c := newCatalog(db)

	a, b := assetsOf(t, "one"), assetsOf(t, "two")
	for _, at := range []Address{a, a, b} {
		require.NoError(t, c.Record(ctx, at))
	}
	got, err := c.Buckets(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []Address{a, b}, got)

	require.Error(t, c.Record(ctx, Address{}))
	require.NoError(t, db.Put(ctx, catalogPrefix+"/not.a.row", []byte{1}))
	_, err = c.Buckets(ctx)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestAShardIsOneOfTheInstancesDatabases(t *testing.T) {
	ctx := t.Context()
	p := peer.Mock(ctx)
	t.Cleanup(p.Close)

	var opened []string
	shards := newDatabaseShards(func(name string) (kvdb.Instance, error) {
		opened = append(opened, name)
		return pkgkvdb.Open(p, "storage/"+name)
	})
	store, err := shards.Open(ctx, 7)
	require.NoError(t, err)
	require.NotNil(t, store)
	require.Equal(t, []string{"shards/s007"}, opened)
	require.NoError(t, shards.Close(ctx, 7, store))
	require.NoError(t, shards.Close(ctx, 7, store), "closing one not open is nothing")

	mock := newDatabaseShards(func(string) (kvdb.Instance, error) { return kvdbmock.New().New(nil, "x", 0) })
	_, err = mock.Open(ctx, 1)
	require.Error(t, err, "a database with no ordered store under it")

	refused := newDatabaseShards(func(string) (kvdb.Instance, error) { return nil, errors.New("disk full") })
	_, err = refused.Open(ctx, 1)
	require.ErrorContains(t, err, "disk full")
}
