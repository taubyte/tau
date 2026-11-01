package pebbleds

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
	"github.com/cockroachdb/pebble/v2/vfs"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	dstest "github.com/ipfs/go-datastore/test"
	gopebble "github.com/ipfs/go-ds-pebble"
)

func open(t testing.TB, opts ...Option) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSuite(t *testing.T) {
	dstest.SubtestAll(t, open(t))
}

func TestNative(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, k := range []string{"/a/1", "/a/2", "/a/3", "/b/1", "/a"} {
		must(t, s.Put(ctx, ds.RawKey(k), []byte("v"+k)))
	}
	var got []string
	must(t, s.Scan([]byte("/a/"), func(k, v []byte) error {
		if !bytes.Equal(v, []byte("v"+string(k))) {
			t.Fatalf("%s holds %q", k, v)
		}
		got = append(got, string(k))
		return nil
	}))
	if !slices.Equal(got, []string{"/a/1", "/a/2", "/a/3"}) {
		t.Fatalf("scan /a/: %v", got)
	}
	got = got[:0]
	must(t, s.Range([]byte("/a/2"), []byte("/b/1"), func(k, _ []byte) error {
		got = append(got, string(k))
		return nil
	}))
	if !slices.Equal(got, []string{"/a/2", "/a/3"}) {
		t.Fatalf("range: %v", got)
	}
	got = got[:0]
	must(t, s.Range(nil, nil, func(k, _ []byte) error {
		got = append(got, string(k))
		return nil
	}))
	if len(got) != 5 {
		t.Fatalf("whole range: %v", got)
	}
	stop := errors.New("stop")
	n := 0
	if err := s.Scan([]byte("/a"), func(_, _ []byte) error { n++; return stop }); !errors.Is(err, stop) || n != 1 {
		t.Fatalf("scan stops on its callback's error: %v after %d", err, n)
	}
	if err := s.View([]byte("/a/1"), func([]byte) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("view returns its callback's error: %v", err)
	}
	if err := s.View([]byte("/none"), func([]byte) error { return nil }); !errors.Is(err, ds.ErrNotFound) {
		t.Fatalf("view of an absent key: %v", err)
	}
	if size, err := s.GetSize(ctx, ds.RawKey("/none")); !errors.Is(err, ds.ErrNotFound) || size != -1 {
		t.Fatalf("size of an absent key: %d %v", size, err)
	}
	if u, err := s.DiskUsage(ctx); err != nil || u == 0 {
		t.Fatalf("disk usage: %d %v", u, err)
	}
}

func TestScanAllFF(t *testing.T) {
	s := open(t)
	b, err := s.NewBatch()
	must(t, err)
	for _, k := range []string{"\xff\xff", "\xff\xff\x01", "\xfe"} {
		must(t, b.Set([]byte(k), nil))
	}
	must(t, b.Commit(context.Background()))
	var got []string
	must(t, s.Scan([]byte("\xff\xff"), func(k, _ []byte) error {
		got = append(got, string(k))
		return nil
	}))
	if !slices.Equal(got, []string{"\xff\xff", "\xff\xff\x01"}) {
		t.Fatalf("scan of an all-0xff prefix: %q", got)
	}
	if up, ok := successor(nil, []byte("a\xff\xff")); !ok || string(up) != "b" {
		t.Fatalf("successor: %q %v", up, ok)
	}
}

func TestBatch(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	must(t, s.Put(ctx, ds.RawKey("/gone"), []byte("x")))
	b, err := s.NewBatch()
	must(t, err)
	k, v := []byte("/k"), []byte("v")
	must(t, b.Set(k, v))
	k[1], v[0] = 'z', 'z'
	must(t, b.Delete(ctx, ds.RawKey("/gone")))
	if has, err := s.Has(ctx, ds.RawKey("/k")); err != nil || has {
		t.Fatalf("a batch writes nothing before Commit: %v %v", has, err)
	}
	must(t, b.Commit(ctx))
	if got, err := s.Get(ctx, ds.RawKey("/k")); err != nil || string(got) != "v" {
		t.Fatalf("a batch copies what it is given: %q %v", got, err)
	}
	if has, err := s.Has(ctx, ds.RawKey("/gone")); err != nil || has {
		t.Fatalf("removed: %v %v", has, err)
	}
	for _, err := range []error{b.Set(k, v), b.Remove(k), b.Put(ctx, ds.RawKey("/k"), v), b.Delete(ctx, ds.RawKey("/k")), b.Commit(ctx)} {
		if !errors.Is(err, ErrCommitted) {
			t.Fatalf("a committed batch refuses: %v", err)
		}
	}
}

func collect(t *testing.T, s *Store, q query.Query) ([]query.Entry, error) {
	t.Helper()
	res, err := s.Query(context.Background(), q)
	must(t, err)
	defer res.Close()
	var out []query.Entry
	for r := range res.Next() {
		if r.Error != nil {
			return out, r.Error
		}
		out = append(out, r.Entry)
	}
	return out, nil
}

func keysOf(es []query.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Key
	}
	return out
}

func TestQuery(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for i, k := range []string{"/q/a", "/q/b", "/q/c", "/q/d", "/r/a"} {
		must(t, s.Put(ctx, ds.RawKey(k), bytes.Repeat([]byte{'x'}, 5-i)))
	}
	cases := []struct {
		q    query.Query
		want []string
	}{
		{query.Query{Prefix: "/q"}, []string{"/q/a", "/q/b", "/q/c", "/q/d"}},
		{query.Query{Prefix: "/q", Orders: []query.Order{&query.OrderByKey{}}}, []string{"/q/a", "/q/b", "/q/c", "/q/d"}},
		{query.Query{Prefix: "/q", Orders: []query.Order{query.OrderByKeyDescending{}}}, []string{"/q/d", "/q/c", "/q/b", "/q/a"}},
		{query.Query{Prefix: "/q", Orders: []query.Order{&query.OrderByKeyDescending{}}, Offset: 1, Limit: 2}, []string{"/q/c", "/q/b"}},
		{query.Query{Prefix: "/q", Orders: []query.Order{query.OrderByValue{}}, Limit: 3}, []string{"/q/d", "/q/c", "/q/b"}},
		{query.Query{Prefix: "/q", Orders: []query.Order{query.OrderByKey{}, query.OrderByValue{}}}, []string{"/q/a", "/q/b", "/q/c", "/q/d"}},
		{query.Query{Prefix: "/q", Filters: []query.Filter{query.FilterKeyCompare{Op: query.NotEqual, Key: "/q/b"}}, Offset: 1}, []string{"/q/c", "/q/d"}},
		{query.Query{Prefix: "/", KeysOnly: true}, []string{"/q/a", "/q/b", "/q/c", "/q/d", "/r/a"}},
		{query.Query{Prefix: "/none"}, nil},
	}
	for _, c := range cases {
		got, err := collect(t, s, c.q)
		must(t, err)
		if !slices.Equal(keysOf(got), c.want) {
			t.Errorf("%v: %v, want %v", c.q, keysOf(got), c.want)
		}
	}
	got, err := collect(t, s, query.Query{Prefix: "/q", KeysOnly: true, ReturnsSizes: true})
	must(t, err)
	for i, e := range got {
		if e.Value != nil || e.Size != 5-i {
			t.Fatalf("keys only with sizes: %+v", e)
		}
	}
	res, err := s.Query(ctx, query.Query{Prefix: "/q"})
	must(t, err)
	must(t, res.Close())
	must(t, res.Close())
	if r, ok := res.NextSync(); ok {
		t.Fatalf("a closed result yields %+v", r)
	}
}

func TestCloseEndsQueries(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	must(t, err)
	for _, k := range []string{"/a", "/b"} {
		must(t, s.Put(ctx, ds.RawKey(k), nil))
	}
	res, err := s.Query(ctx, query.Query{})
	must(t, err)
	if r, ok := res.NextSync(); !ok || r.Error != nil {
		t.Fatalf("first: %+v %v", r, ok)
	}
	must(t, s.Close())
	if r, ok := res.NextSync(); !ok || !errors.Is(r.Error, ErrClosed) {
		t.Fatalf("a query Close ended reports it: %+v %v", r, ok)
	}
	if _, ok := res.NextSync(); ok {
		t.Fatal("then ends")
	}
	must(t, res.Close())
}

func TestClosed(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	must(t, err)
	b, err := s.NewBatch()
	must(t, err)
	must(t, b.Set([]byte("/k"), nil))
	must(t, s.Close())
	must(t, s.Close())
	k := ds.RawKey("/k")
	_, getErr := s.Get(ctx, k)
	_, hasErr := s.Has(ctx, k)
	_, sizeErr := s.GetSize(ctx, k)
	_, queryErr := s.Query(ctx, query.Query{})
	_, batchErr := s.Batch(ctx)
	_, usageErr := s.DiskUsage(ctx)
	for i, err := range []error{
		getErr, hasErr, sizeErr, queryErr, batchErr, usageErr,
		s.Put(ctx, k, nil), s.Delete(ctx, k), s.Sync(ctx, k), b.Commit(ctx),
		s.Scan(nil, func(_, _ []byte) error { return nil }),
	} {
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("call %d after Close: %v", i, err)
		}
	}
}

func TestCloseWaitsForCalls(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	must(t, err)
	must(t, s.Put(ctx, ds.RawKey("/k"), []byte("v")))
	in, release := make(chan struct{}), make(chan struct{})
	scanned := make(chan error, 1)
	go func() {
		scanned <- s.Scan(nil, func(_, _ []byte) error {
			close(in)
			<-release
			return s.View([]byte("/k"), func([]byte) error { return nil })
		})
	}()
	<-in
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	for !s.closed.Load() {
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-closed:
		t.Fatalf("Close returned while a scan ran: %v", err)
	default:
	}
	close(release)
	if err := <-scanned; !errors.Is(err, ErrClosed) {
		t.Fatalf("a call begun after Close began is refused: %v", err)
	}
	must(t, <-closed)
}

func TestSyncSurvivesCrash(t *testing.T) {
	ctx := context.Background()
	fs := vfs.NewCrashableMem()
	s, err := Open("db", WithPebbleOptions(&pebble.Options{FS: fs}))
	must(t, err)
	must(t, s.Put(ctx, ds.RawKey("/synced"), []byte("1")))
	must(t, s.Sync(ctx, ds.RawKey("/")))
	must(t, s.Put(ctx, ds.RawKey("/unsynced"), []byte("2")))
	crashed := fs.CrashClone(vfs.CrashCloneCfg{})
	must(t, s.Close())
	r, err := Open("db", WithPebbleOptions(&pebble.Options{FS: crashed}))
	must(t, err)
	defer func() { must(t, r.Close()) }()
	if got, err := r.Get(ctx, ds.RawKey("/synced")); err != nil || string(got) != "1" {
		t.Fatalf("a write before Sync survives a crash: %q %v", got, err)
	}
	if has, err := r.Has(ctx, ds.RawKey("/unsynced")); err != nil || has {
		t.Fatalf("an unsynced write is not durable: %v %v", has, err)
	}
}

func TestSyncedWrites(t *testing.T) {
	ctx := context.Background()
	fs := vfs.NewCrashableMem()
	s, err := Open("db", WithPebbleOptions(&pebble.Options{FS: fs}), WithWriteOptions(pebble.Sync))
	must(t, err)
	must(t, s.Put(ctx, ds.RawKey("/k"), []byte("1")))
	crashed := fs.CrashClone(vfs.CrashCloneCfg{})
	must(t, s.Close())
	r, err := Open("db", WithPebbleOptions(&pebble.Options{FS: crashed}))
	must(t, err)
	defer func() { must(t, r.Close()) }()
	if has, err := r.Has(ctx, ds.RawKey("/k")); err != nil || !has {
		t.Fatalf("a synced write survives a crash: %v %v", has, err)
	}
}

func TestNoWAL(t *testing.T) {
	s := open(t, WithPebbleOptions(&pebble.Options{DisableWAL: true}))
	must(t, s.Sync(context.Background(), ds.RawKey("/")))
}

func TestOpenFails(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	must(t, os.WriteFile(f, nil, 0o600))
	if _, err := Open(f); err == nil {
		t.Fatal("opened a file as a database")
	}
}

func TestSameLayoutAsGoDsPebble(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	theirs, err := gopebble.NewDatastore(dir)
	must(t, err)
	must(t, theirs.Put(ctx, ds.NewKey("/a/b"), []byte("theirs")))
	must(t, theirs.Close())
	ours, err := Open(dir)
	must(t, err)
	if got, err := ours.Get(ctx, ds.NewKey("/a/b")); err != nil || string(got) != "theirs" {
		t.Fatalf("%q %v", got, err)
	}
	must(t, ours.Put(ctx, ds.NewKey("/a/c"), []byte("ours")))
	must(t, ours.Close())
	theirs, err = gopebble.NewDatastore(dir)
	must(t, err)
	defer func() { must(t, theirs.Close()) }()
	if got, err := theirs.Get(ctx, ds.NewKey("/a/c")); err != nil || string(got) != "ours" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestConcurrent(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 200 {
				k := ds.RawKey("/c/" + string(rune('a'+w)) + "/" + string(rune('a'+i%26)))
				if err := s.Put(ctx, k, []byte{byte(i)}); err != nil {
					t.Error(err)
					return
				}
				if err := s.Scan([]byte("/c/"), func(_, _ []byte) error { return nil }); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestBatchReset(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	b, err := s.NewBatch()
	must(t, err)
	if b.Store() != s {
		t.Fatal("a batch names its store")
	}
	must(t, b.Set([]byte("/dropped"), []byte("x")))
	must(t, b.Reset())
	must(t, b.Set([]byte("/k"), []byte("v")))
	must(t, b.Commit(ctx))
	if has, err := s.Has(ctx, ds.RawKey("/dropped")); err != nil || has {
		t.Fatalf("Reset drops what the batch held: %v %v", has, err)
	}
	if w := s.Written(); w != int64(len("/k")+len("v")) {
		t.Fatalf("written %d", w)
	}
	must(t, b.Reset())
	must(t, b.Set([]byte("/k2"), []byte("v2")))
	must(t, b.Commit(ctx))
	if got, err := s.Get(ctx, ds.RawKey("/k2")); err != nil || string(got) != "v2" {
		t.Fatalf("a committed batch, reset, writes again: %q %v", got, err)
	}
	must(t, s.Put(ctx, ds.RawKey("/p"), []byte("1")))
	if w := s.Written(); w != int64(len("/k")+len("v")+len("/k2")+len("v2")+len("/p")+len("1")) {
		t.Fatalf("written %d", w)
	}
}

func TestBatchResetClosed(t *testing.T) {
	s, err := Open(t.TempDir())
	must(t, err)
	b, err := s.NewBatch()
	must(t, err)
	must(t, b.Commit(context.Background()))
	must(t, s.Close())
	if err := b.Reset(); !errors.Is(err, ErrClosed) {
		t.Fatalf("reset over a closed store: %v", err)
	}
}

func TestScanEverythingAfterAPrefix(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, k := range []string{"/a", "/b"} {
		must(t, s.Put(ctx, ds.RawKey(k), nil))
	}
	for range 3 {
		must(t, s.Scan([]byte("/a"), func(_, _ []byte) error { return nil }))
		n := 0
		must(t, s.Scan(nil, func(_, _ []byte) error { n++; return nil }))
		must(t, s.Scan([]byte{}, func(_, _ []byte) error { n++; return nil }))
		if n != 4 {
			t.Fatalf("an empty prefix scanned %d keys twice, want 2 each", n)
		}
	}
}

func TestBloomFilters(t *testing.T) {
	o := &pebble.Options{FS: vfs.NewMem()}
	s, err := Open("db", WithPebbleOptions(o))
	must(t, err)
	must(t, s.Close())
	if o.Levels[0].FilterPolicy != bloom.FilterPolicy(10) {
		t.Fatalf("filter %v", o.Levels[0].FilterPolicy)
	}
	o = &pebble.Options{FS: vfs.NewMem(), Levels: [7]pebble.LevelOptions{{FilterPolicy: pebble.NoFilterPolicy}}}
	s, err = Open("db", WithPebbleOptions(o))
	must(t, err)
	must(t, s.Close())
	if o.Levels[0].FilterPolicy != pebble.NoFilterPolicy {
		t.Fatalf("a filter the caller chose was replaced: %v", o.Levels[0].FilterPolicy)
	}
}

// pebble's own cache, made from CacheSize, keeps nothing.
func TestBlockCacheKeeps(t *testing.T) {
	ctx := context.Background()
	for _, o := range []*pebble.Options{{}, {CacheSize: 32 << 20}} {
		s := open(t, WithPebbleOptions(o))
		b, err := s.NewBatch()
		must(t, err)
		for i := range 10000 {
			must(t, b.Set([]byte(fmt.Sprintf("/k/%05d", i)), []byte("v")))
		}
		must(t, b.Commit(ctx))
		must(t, s.db.Flush())
		for range 100 {
			must(t, s.View([]byte("/k/00042"), func([]byte) error { return nil }))
		}
		if m := s.db.Metrics().BlockCache; m.Hits == 0 || m.Count == 0 {
			t.Fatalf("CacheSize %d: the block cache kept nothing: %+v", o.CacheSize, m)
		}
	}
}
