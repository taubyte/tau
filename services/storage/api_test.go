package storage

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ipfs/go-cid"
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

func TestAStoreIsDialledAsTheStore(t *testing.T) {
	ctx := t.Context()
	at := assetsOf(t, "objects")
	c := newCluster(t, 3, fixedCatalog{at})

	s, err := Dial(c.caller, c.ids[0])
	require.NoError(t, err)
	t.Cleanup(func() { s.(*Client).Close() })
	v, err := s.Put(ctx, at, "o", Object{}, strings.NewReader("over the wire"))
	require.NoError(t, err)
	vs, err := s.Versions(ctx, at, "o", 0)
	require.NoError(t, err)
	require.Len(t, vs, 1)

	// A SOURCE THAT FAILS is the put's failure, and nothing is written.
	_, err = s.Put(ctx, at, "broken", Object{}, io.MultiReader(strings.NewReader("some"), erroring{}))
	require.Error(t, err)
	_, err = s.Current(ctx, at, "broken")
	require.ErrorIs(t, err, ErrNotFound)

	// AND IN PROCESS, the same store.
	here := c.nodes[1]
	vs, err = here.Versions(ctx, at, "o", 0)
	require.NoError(t, err)
	require.Equal(t, v.Stamp, vs[0].Stamp)
	listed, err := here.List(ctx, at, "", "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"o"}, names(listed))
	_, err = here.Delete(ctx, at, "o")
	require.NoError(t, err)
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
