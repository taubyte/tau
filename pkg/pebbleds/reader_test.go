package pebbleds

import (
	"context"
	"errors"
	"slices"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

func scanned(t *testing.T, r *Reader, prefix string) []string {
	t.Helper()
	var got []string
	must(t, r.Scan([]byte(prefix), func(k, v []byte) error {
		if string(v) != "v"+string(k) {
			t.Fatalf("%s holds %q", k, v)
		}
		got = append(got, string(k))
		return nil
	}))
	return got
}

func viewed(t *testing.T, r *Reader, key string) (string, bool) {
	t.Helper()
	var got string
	err := r.View([]byte(key), func(v []byte) error {
		got = string(v)
		return nil
	})
	if errors.Is(err, ds.ErrNotFound) {
		return "", false
	}
	must(t, err)
	return got, true
}

func TestAReaderReadsTheStoreAsItWas(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for _, k := range []string{"/a/1", "/a/2", "/b/1", "/a"} {
		must(t, s.Put(ctx, ds.RawKey(k), []byte("v"+k)))
	}
	r, err := s.NewReader()
	must(t, err)
	must(t, s.Put(ctx, ds.RawKey("/a/3"), []byte("v/a/3")))
	must(t, s.Delete(ctx, ds.RawKey("/a/1")))
	if got := scanned(t, r, "/a/"); !slices.Equal(got, []string{"/a/1", "/a/2"}) {
		t.Fatalf("a reader scanned %q, want what the store held when it was made", got)
	}
	// A key outside the last scan's bounds is read all the same.
	for _, k := range []string{"/b/1", "/a", "/a/1"} {
		if v, ok := viewed(t, r, k); !ok || v != "v"+k {
			t.Fatalf("%s: %q %v", k, v, ok)
		}
	}
	for _, k := range []string{"/a/3", "/a/", "/c"} {
		if _, ok := viewed(t, r, k); ok {
			t.Fatalf("%s read, and the store did not hold it", k)
		}
	}
	if got := scanned(t, r, ""); !slices.Equal(got, []string{"/a", "/a/1", "/a/2", "/b/1"}) {
		t.Fatalf("a scan of everything: %q", got)
	}
	must(t, r.Close())
	r, err = s.NewReader()
	must(t, err)
	if got := scanned(t, r, "/a/"); !slices.Equal(got, []string{"/a/2", "/a/3"}) {
		t.Fatalf("a new reader scanned %q", got)
	}
	must(t, r.Close())
}

func TestAReaderScansAnAllFFPrefix(t *testing.T) {
	s := open(t)
	b, err := s.NewBatch()
	must(t, err)
	for _, k := range []string{"\xff\xff", "\xff\xff\x01", "\xfe"} {
		must(t, b.Set([]byte(k), []byte("v"+k)))
	}
	must(t, b.Commit(context.Background()))
	r, err := s.NewReader()
	must(t, err)
	defer r.Close()
	if got := scanned(t, r, "\xff\xff"); !slices.Equal(got, []string{"\xff\xff", "\xff\xff\x01"}) {
		t.Fatalf("scan of an all-0xff prefix: %q", got)
	}
}

func TestAReaderHandsBackWhatItsCallbackReturns(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	must(t, s.Put(ctx, ds.RawKey("/k"), nil))
	r, err := s.NewReader()
	must(t, err)
	defer r.Close()
	stop := errors.New("stop")
	if err := r.View([]byte("/k"), func([]byte) error { return stop }); err != stop {
		t.Fatalf("view: %v", err)
	}
	if err := r.Scan([]byte("/"), func(_, _ []byte) error { return stop }); err != stop {
		t.Fatalf("scan: %v", err)
	}
}

func TestCloseEndsReaders(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	must(t, err)
	must(t, s.Put(ctx, ds.RawKey("/k"), nil))
	r, err := s.NewReader()
	must(t, err)
	must(t, s.Close())
	if err := r.View([]byte("/k"), func([]byte) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("view after Close: %v", err)
	}
	if err := r.Scan(nil, func(_, _ []byte) error { return nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("scan after Close: %v", err)
	}
	must(t, r.Close())
	if _, err := s.NewReader(); !errors.Is(err, ErrClosed) {
		t.Fatalf("a reader made after Close: %v", err)
	}
}
