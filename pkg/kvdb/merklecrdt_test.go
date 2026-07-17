package kvdb

// Tests for merklecrdt.go's advanced MerkleCRDT wrapper (Item H):
// NewMerkleCRDT (default + custom internalOptions + error propagation from
// Options.verify), Publish, Set, and the multi-root/no-root branches of
// Traverse that TestCRDTHeadsPublish (single root) does not exercise.

import (
	"context"
	"strings"
	"testing"

	"github.com/ipfs/boxo/ipld/merkledag"
	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// newTestMerkleCRDT builds a standalone MerkleCRDT (not part of the
// makeNReplicas harness) using a no-op broadcaster, for tests that only
// care about the local API surface.
func newTestMerkleCRDT(t *testing.T, opts *Options, internalOpts *MerkleCRDTOptions) *MerkleCRDT {
	t.Helper()
	bs := mdutils.Bserv()
	dagserv := merkledag.NewDAGService(bs)
	dagsync := &mockDAGSvc{DAGService: dagserv, bs: bs.Blockstore()}

	bcasts, cancel := newBroadcasters(t, 1)
	t.Cleanup(cancel)

	mcrdt, err := NewMerkleCRDT(
		dssyncMap(),
		ds.NewKey("merklecrdt-test"),
		dagsync,
		bcasts[0],
		opts,
		internalOpts,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mcrdt.Close(); err != nil {
			t.Error(err)
		}
	})
	return mcrdt
}

// TestNewMerkleCRDTDefaults checks that NewMerkleCRDT with nil internalOptions
// behaves exactly like NewDatastore(): default namespaces, a usable Put/Get.
func TestNewMerkleCRDTDefaults(t *testing.T) {
	mcrdt := newTestMerkleCRDT(t, nil, nil)
	ctx := context.Background()

	k := ds.NewKey("mcrdt-default-key")
	if err := mcrdt.Put(ctx, k, []byte("v")); err != nil {
		t.Fatal(err)
	}
	got, err := mcrdt.Get(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v" {
		t.Fatalf("expected 'v', got %q", got)
	}
}

// TestNewMerkleCRDTCustomInternalOptions exercises every branch of
// NewMerkleCRDT's internalOptions handling: a custom DeltaFactory and every
// custom Namespaces field, then checks the store is actually usable with
// those namespaces (the custom heads namespace shows up under the given
// name).
func TestNewMerkleCRDTCustomInternalOptions(t *testing.T) {
	usedFactory := false
	internalOpts := &MerkleCRDTOptions{
		DeltaFactory: func() Delta {
			usedFactory = true
			return &pbDelta{Delta: &pb.Delta{}}
		},
		Namespaces: InternalNamespaces{
			Heads:           "custom-heads",
			DAGHeads:        "custom-dagheads",
			Set:             "custom-set",
			ProcessedBlocks: "custom-blocks",
			DirtyBitKey:     "custom-dirty",
			BadShutdownKey:  "custom-badshutdown",
			VersionKey:      "custom-version",
			Reclaim:         "custom-reclaim",
		},
	}

	mcrdt := newTestMerkleCRDT(t, nil, internalOpts)
	ctx := context.Background()

	k := ds.NewKey("mcrdt-custom-key")
	if err := mcrdt.Put(ctx, k, []byte("custom-value")); err != nil {
		t.Fatal(err)
	}
	if !usedFactory {
		t.Error("expected the custom DeltaFactory to have been used")
	}

	got, err := mcrdt.Get(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "custom-value" {
		t.Fatalf("expected 'custom-value', got %q", got)
	}

	// The custom heads namespace should actually be the one in use.
	if !strings.Contains(mcrdt.heads.namespace.String(), "custom-heads") {
		t.Errorf("expected heads namespace to contain 'custom-heads', got %s", mcrdt.heads.namespace.String())
	}
}

// TestNewMerkleCRDTInvalidOptions checks that NewMerkleCRDT propagates the
// error returned by Options.verify() rather than swallowing it.
func TestNewMerkleCRDTInvalidOptions(t *testing.T) {
	bs := mdutils.Bserv()
	dagserv := merkledag.NewDAGService(bs)
	dagsync := &mockDAGSvc{DAGService: dagserv, bs: bs.Blockstore()}
	bcasts, cancel := newBroadcasters(t, 1)
	defer cancel()

	badOpts := DefaultOptions()
	badOpts.NumWorkers = 0 // invalid

	_, err := NewMerkleCRDT(
		dssyncMap(),
		ds.NewKey("merklecrdt-bad-opts"),
		dagsync,
		bcasts[0],
		badOpts,
		nil,
	)
	if err == nil {
		t.Fatal("expected an error constructing NewMerkleCRDT with invalid Options")
	}
}

// TestMerkleCRDTPublishAndSet checks Publish (manual delta submission) and
// Set (access to the internal Set), independent of Put/Delete.
func TestMerkleCRDTPublishAndSet(t *testing.T) {
	mcrdt := newTestMerkleCRDT(t, nil, nil)
	ctx := context.Background()

	s := mcrdt.Set()
	if s == nil {
		t.Fatal("expected a non-nil Set")
	}

	delta, err := s.Add(ctx, ds.NewKey("published-key").String(), []byte("published-value"))
	if err != nil {
		t.Fatal(err)
	}

	head, err := mcrdt.Publish(ctx, delta)
	if err != nil {
		t.Fatal(err)
	}
	if !head.Cid.Defined() {
		t.Fatal("expected Publish to return a defined head CID")
	}

	got, err := mcrdt.Get(ctx, ds.NewKey("published-key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "published-value" {
		t.Fatalf("expected 'published-value', got %q", got)
	}

	heads, _, err := mcrdt.Heads().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range heads {
		if h.Cid == head.Cid {
			found = true
		}
	}
	if !found {
		t.Error("expected the published head to be among the current heads")
	}
}

// TestTraverseNoRoots checks Traverse's error branch when called with no
// roots at all.
func TestTraverseNoRoots(t *testing.T) {
	mcrdt := newTestMerkleCRDT(t, nil, nil)
	ctx := context.Background()

	err := mcrdt.Traverse(ctx, nil, func(ipld.Node) error { return nil })
	if err == nil {
		t.Fatal("expected an error traversing with no roots")
	}
}

// TestTraverseMultipleRoots checks Traverse's multi-root branch: it builds a
// synthetic root node linking to every given CID and visits from there,
// skipping the synthetic root itself (ignoreCid) so only the real roots (and
// anything below them) are visited.
//
// A plain sequence of Puts to the same (default) dagName all chain onto a
// single head, so two distinct dagNames are used here (as elsewhere in the
// suite, e.g. TestCRDTHeadsPublish) to get two independent heads.
func TestTraverseMultipleRoots(t *testing.T) {
	mcrdt := newTestMerkleCRDT(t, nil, nil)
	ctx := context.Background()

	delta1, err := mcrdt.Set().Add(ctx, ds.NewKey("root-a").String(), []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	delta1.SetDagName("dag1")
	if _, err := mcrdt.Publish(ctx, delta1); err != nil {
		t.Fatal(err)
	}

	delta2, err := mcrdt.Set().Add(ctx, ds.NewKey("root-b").String(), []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	delta2.SetDagName("dag2")
	if _, err := mcrdt.Publish(ctx, delta2); err != nil {
		t.Fatal(err)
	}

	heads, _, err := mcrdt.Heads().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) < 2 {
		t.Fatalf("expected at least 2 heads for a multi-root traversal, got %d", len(heads))
	}

	roots := make([]cid.Cid, len(heads))
	for i, h := range heads {
		roots[i] = h.Cid
	}

	visited := map[cid.Cid]bool{}
	err = mcrdt.Traverse(ctx, roots, func(n ipld.Node) error {
		visited[n.Cid()] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range roots {
		if !visited[r] {
			t.Errorf("expected root %s to have been visited", r)
		}
	}
}

// dssyncMap returns a fresh in-memory synchronized datastore, matching what
// makeStore(t, mapStore) would give, without depending on the *testing.T
// passed to makeStore.
func dssyncMap() ds.Datastore {
	return dssync.MutexWrap(ds.NewMapDatastore())
}
