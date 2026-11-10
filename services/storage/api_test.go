package storage

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer/peertest"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	storagev2 "github.com/taubyte/tau/pkg/proto/gen/tau/storage/v2"
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

	// FROM EVERY NODE, a holder of the shard or not, and over the wire, once the
	// rows a write reached its barrier without have landed.
	for i := range c.nodes {
		var all []Version
		require.Eventually(t, func() bool {
			var err error
			all, err = c.client(t, i, i).List(ctx, at, "", "", 0)
			return err == nil && slices.Equal([]string{"a/1", "a/2", "ab"}, names(all))
		}, 10*time.Second, 10*time.Millisecond, "node %d: a deleted name is listed, or a live one is not", i)
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

func TestAPutIsKeptOnlyWhenItsEndSaysItIsWhole(t *testing.T) {
	ctx := t.Context()
	at := assetsOf(t, "objects")
	c := newCluster(t, 3, fixedCatalog{at})
	cl, err := NewClient(c.caller, c.ids[0])
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })

	head := func(name string) *storagev2.PutRequest {
		return &storagev2.PutRequest{Part: &storagev2.PutRequest_Head{Head: &storagev2.PutHead{At: wireAddress(at), Name: []byte(name)}}}
	}
	chunk := &storagev2.PutRequest{Part: &storagev2.PutRequest_Chunk{Chunk: []byte("some")}}
	end := func(size uint64) *storagev2.PutRequest {
		return &storagev2.PutRequest{Part: &storagev2.PutRequest_End{End: &storagev2.PutEnd{Size: size}}}
	}
	put := func(name string, parts ...*storagev2.PutRequest) error {
		stream := cl.svc.Put(ctx)
		for _, p := range append([]*storagev2.PutRequest{head(name)}, parts...) {
			// A send the server stopped taking is its answer, which the close returns.
			if stream.Send(p) != nil {
				break
			}
		}
		_, err := stream.CloseAndReceive()
		return err
	}

	// A STREAM THAT DOES NOT END WITH ITS END is refused, and nothing of it is kept.
	for name, parts := range map[string][]*storagev2.PutRequest{
		"closed after a chunk":  {chunk},
		"closed after its head": nil,
		"ended short":           {chunk, end(5)},
		"ended long":            {chunk, end(3)},
		"sent on after its end": {chunk, end(4), chunk},
		"headed twice":          {chunk, head("headed twice"), end(4)},
	} {
		err := put(name, parts...)
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "%s: %v", name, err)
		require.ErrorContains(t, err, ErrIncompletePut.Error(), name)
		_, err = cl.Current(ctx, at, name)
		require.ErrorIs(t, err, ErrNotFound, name)
	}

	// AN END THAT COUNTS WHAT CAME is the object, an empty one too.
	require.NoError(t, put("whole", chunk, end(4)))
	require.NoError(t, put("empty", end(0)))
	whole, err := cl.Current(ctx, at, "whole")
	require.NoError(t, err)
	require.EqualValues(t, 4, whole.Record.Size)
	empty, err := cl.Current(ctx, at, "empty")
	require.NoError(t, err)
	require.Zero(t, empty.Record.Size)
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
	mn := peertest.Net(t)
	ctx := t.Context()
	p := mn.Mock(ctx)
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
