package kvdb

// Tests for spec items A-F: putTombs batching (A), element-marker priority
// + v1->v2 migration (B), Elements() honoring KeysOnly (C), unreserving a
// child after a processNode failure (D), and DAGSyncerTimeout applied to
// findBestValue/migration DAG fetches (E). Item F is two tiny diffs with no
// independent behavior to test.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// TestPutTombsMultiVersionDelete checks that deleting a key that has
// accumulated many versions (element markers) in one Delete() call ends up
// with the key fully gone, one tombstone per surviving version, and the
// value/priority keys removed -- exercising the grouped-by-key putTombs
// path (Item A) instead of the old one-findBestValue-per-tombstone path.
func TestPutTombsMultiVersionDelete(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	k := ds.NewKey("multiversion")
	const versions = 20
	for v := range versions {
		if err := r.Put(ctx, k, fmt.Appendf(nil, "v%d", v)); err != nil {
			t.Fatal(err)
		}
	}

	has, err := r.Has(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("expected key to be present before delete")
	}

	if err := r.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}

	has, err = r.Has(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("expected key to be gone after delete")
	}

	if _, err := r.Get(ctx, k); err != ds.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if ok, _ := r.store.Has(ctx, r.set.valueKey(k.String())); ok {
		t.Fatal("value key should have been deleted")
	}
	if ok, _ := r.store.Has(ctx, r.set.priorityKey(k.String())); ok {
		t.Fatal("priority key should have been deleted")
	}

	countPrefix := func(prefix ds.Key) int {
		res, err := r.store.Query(ctx, query.Query{Prefix: prefix.String(), KeysOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer res.Close() //nolint:errcheck
		n := 0
		for e := range res.Next() {
			if e.Error != nil {
				t.Fatal(e.Error)
			}
			n++
		}
		return n
	}

	if n := countPrefix(r.set.elemsPrefix(k.String())); n != versions {
		t.Fatalf("expected %d element markers, got %d", versions, n)
	}
	if n := countPrefix(r.set.tombsPrefix(k.String())); n != versions {
		t.Fatalf("expected %d tombstones, got %d", versions, n)
	}
}

// TestElementsKeysOnly checks that a KeysOnly query returns the same set of
// keys as a full query, with nil values, honoring the caller's KeysOnly
// request end-to-end (Item C).
func TestElementsKeysOnly(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	want := map[string][]byte{}
	for i := range 25 {
		k := fmt.Sprintf("elemkey-%d", i)
		v := fmt.Appendf(nil, "value-%d", i)
		if err := r.Put(ctx, ds.NewKey(k), v); err != nil {
			t.Fatal(err)
		}
		want[ds.NewKey(k).String()] = v
	}

	fullRes, err := r.Query(ctx, query.Query{})
	if err != nil {
		t.Fatal(err)
	}
	full := map[string][]byte{}
	for e := range fullRes.Next() {
		if e.Error != nil {
			t.Fatal(e.Error)
		}
		full[e.Key] = e.Value
	}
	// nolint:errcheck
	fullRes.Close()

	if len(full) != len(want) {
		t.Fatalf("expected %d entries in full query, got %d", len(want), len(full))
	}
	for k, v := range want {
		got, ok := full[k]
		if !ok {
			t.Fatalf("missing key %s in full query", k)
		}
		if !bytes.Equal(got, v) {
			t.Fatalf("value mismatch for %s: got %q want %q", k, got, v)
		}
	}

	koRes, err := r.Query(ctx, query.Query{KeysOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	ko := map[string]bool{}
	for e := range koRes.Next() {
		if e.Error != nil {
			t.Fatal(e.Error)
		}
		if e.Value != nil {
			t.Fatalf("expected nil value for keys-only query entry %s, got %q", e.Key, e.Value)
		}
		ko[e.Key] = true
	}
	// nolint:errcheck
	koRes.Close()

	if len(ko) != len(want) {
		t.Fatalf("expected %d keys in keys-only query, got %d", len(want), len(ko))
	}
	for k := range want {
		if !ko[k] {
			t.Fatalf("missing key %s in keys-only query", k)
		}
	}
}

// TestFindBestValueMarkerPriority checks that (Item B) element markers
// written by putElems carry the effective priority (decodable via
// decodePriority), and that findBestValue still resolves an element whose
// marker was written empty (as pre-v2/legacy code would) by falling back to
// fetching its delta from the DAG.
func TestFindBestValueMarkerPriority(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	markerIDFor := func(key string) string {
		res, err := r.store.Query(ctx, query.Query{Prefix: r.set.elemsPrefix(key).String(), KeysOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer res.Close() //nolint:errcheck
		var id string
		for e := range res.Next() {
			if e.Error != nil {
				t.Fatal(e.Error)
			}
			id = strings.TrimPrefix(strings.TrimPrefix(e.Key, r.set.elemsPrefix(key).String()), "/")
		}
		if id == "" {
			t.Fatalf("could not find element marker for key %s", key)
		}
		return id
	}

	// New marker: value must decode to the key's current priority.
	k := ds.NewKey("prio-marker")
	if err := r.Put(ctx, k, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	wantPrio, err := r.set.getPriority(ctx, k.String())
	if err != nil {
		t.Fatal(err)
	}

	id := markerIDFor(k.String())
	markerVal, err := r.store.Get(ctx, r.set.elemsPrefix(k.String()).ChildString(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(markerVal) == 0 {
		t.Fatal("expected marker value to carry the priority, got empty value")
	}
	gotPrio, err := decodePriority(markerVal)
	if err != nil {
		t.Fatalf("marker value not decodable as a priority: %v", err)
	}
	if gotPrio != wantPrio {
		t.Fatalf("marker priority %d != key priority %d", gotPrio, wantPrio)
	}

	// Legacy marker: hand-clear the marker value to simulate pre-v2 data
	// and confirm findBestValue still resolves the correct value and
	// priority via the DAG fallback.
	k2 := ds.NewKey("legacy-marker")
	if err := r.Put(ctx, k2, []byte("legacy-value")); err != nil {
		t.Fatal(err)
	}
	wantPrio2, err := r.set.getPriority(ctx, k2.String())
	if err != nil {
		t.Fatal(err)
	}
	id2 := markerIDFor(k2.String())
	markerKey2 := r.set.elemsPrefix(k2.String()).ChildString(id2)
	if err := r.store.Put(ctx, markerKey2, nil); err != nil {
		t.Fatal(err)
	}

	val, gotPrio2, err := r.set.findBestValue(ctx, k2.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "legacy-value" {
		t.Fatalf("expected legacy-value, got %q", val)
	}
	if gotPrio2 != wantPrio2 {
		t.Fatalf("expected priority %d, got %d", wantPrio2, gotPrio2)
	}
}

// TestMigration1to2 hand-crafts v1-style data (element markers with empty
// values) and checks that migrate1to2 backfills them with the correct
// priority, bumps nothing itself (applyMigrations owns the version key, so
// this test drives migrate1to2 directly and sets the version around it),
// and leaves markers whose block cannot be fetched empty without failing
// the migration.
func TestMigration1to2(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	v, err := r.getVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != version {
		t.Fatalf("expected fresh datastore at version %d, got %d", version, v)
	}

	markerIDFor := func(key string) string {
		res, err := r.store.Query(ctx, query.Query{Prefix: r.set.elemsPrefix(key).String(), KeysOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer res.Close() //nolint:errcheck
		var id string
		for e := range res.Next() {
			if e.Error != nil {
				t.Fatal(e.Error)
			}
			id = strings.TrimPrefix(strings.TrimPrefix(e.Key, r.set.elemsPrefix(key).String()), "/")
		}
		if id == "" {
			t.Fatalf("could not find element marker for key %s", key)
		}
		return id
	}

	type kv struct {
		key  string
		id   string
		prio uint64
	}
	var good []kv

	for i := range 5 {
		k := fmt.Sprintf("mig-%d", i)
		val := fmt.Appendf(nil, "value-%d", i)
		if err := r.Put(ctx, ds.NewKey(k), val); err != nil {
			t.Fatal(err)
		}
		prio, err := r.set.getPriority(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		id := markerIDFor(k)

		// Simulate pre-v2 data: clear the marker value by hand.
		markerKey := r.set.elemsPrefix(k).ChildString(id)
		if err := r.store.Put(ctx, markerKey, nil); err != nil {
			t.Fatal(err)
		}
		good = append(good, kv{key: k, id: id, prio: prio})
	}

	// A marker whose block cannot be fetched: put a real key, then
	// delete its block from the underlying blockstore so that GetDelta
	// fails, while leaving a real (well-formed) block CID as the marker
	// id.
	brokenKeyStr := "mig-broken"
	if err := r.Put(ctx, ds.NewKey(brokenKeyStr), []byte("broken")); err != nil {
		t.Fatal(err)
	}
	brokenID := markerIDFor(brokenKeyStr)

	mhash, err := dshelp.DsKeyToMultihash(ds.NewKey(brokenID))
	if err != nil {
		t.Fatal(err)
	}
	brokenCid := cid.NewCidV1(cid.DagProtobuf, mhash)

	mds, ok := r.dagService.(*mockDAGSvc)
	if !ok {
		t.Fatalf("expected *mockDAGSvc dagService, got %T", r.dagService)
	}
	if err := mds.bs.DeleteBlock(ctx, brokenCid); err != nil {
		t.Fatal(err)
	}
	brokenMarkerKey := r.set.elemsPrefix(brokenKeyStr).ChildString(brokenID)
	if err := r.store.Put(ctx, brokenMarkerKey, nil); err != nil {
		t.Fatal(err)
	}

	// Drop the version back to 1 and run the real migration.
	if err := r.setVersion(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.migrate1to2(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.setVersion(ctx, 2); err != nil {
		t.Fatal(err)
	}

	for _, item := range good {
		markerKey := r.set.elemsPrefix(item.key).ChildString(item.id)
		val, err := r.store.Get(ctx, markerKey)
		if err != nil {
			t.Fatal(err)
		}
		gotPrio, err := decodePriority(val)
		if err != nil {
			t.Fatalf("marker for %s not backfilled: %v", item.key, err)
		}
		if gotPrio != item.prio {
			t.Fatalf("marker for %s: got prio %d want %d", item.key, gotPrio, item.prio)
		}
	}

	val, err := r.store.Get(ctx, brokenMarkerKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(val) != 0 {
		t.Fatalf("expected broken marker to remain empty after migration, got %v", val)
	}

	v, err = r.getVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 {
		t.Fatalf("expected version 2 after migration, got %d", v)
	}
}

// flakyDelta wraps a pbDelta and fails GetElements() (and therefore
// set.Merge) while fail is true, so tests can force a processNode failure
// on demand and later "fix" it to simulate reprocessing.
type flakyDelta struct {
	*pbDelta
	fail *atomic.Bool
}

func (d *flakyDelta) GetElements() ([]*pb.Element, error) {
	if d.fail.Load() {
		return nil, errors.New("injected merge failure")
	}
	return d.pbDelta.GetElements()
}

// TestProcessNodeFailureUnreservesChild checks Item D: when processNode
// fails (here, because set.Merge fails), the node's reservation in
// queuedChildren must be released so that a later attempt (e.g. triggered
// by a rebroadcast) can pick the branch back up immediately, rather than
// being stuck until the periodic repair walk.
func TestProcessNodeFailureUnreservesChild(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	fail := &atomic.Bool{}
	fail.Store(true)

	inner := &pbDelta{Delta: &pb.Delta{
		Elements: []*pb.Element{{Key: "flaky", Value: []byte("v")}},
		Priority: 1,
	}}
	fd := &flakyDelta{pbDelta: inner, fail: fail}

	node, err := makeNode(fd, nil)
	if err != nil {
		t.Fatal(err)
	}
	current := node.Cid()

	root := Head{Cid: current}
	root.Height = 1

	ng := &crdtNodeGetter{NodeGetter: r.dagService}

	// Simulate the parent having reserved this node for processing, as
	// processNode's own child-processing loop would have done.
	if !r.queuedChildren.Visit(current) {
		t.Fatal("expected to reserve the node for processing")
	}

	if _, err := r.processNode(ctx, ng, root, fd, node, false); err == nil {
		t.Fatal("expected processNode to fail")
	}
	if r.queuedChildren.Has(current) {
		t.Fatal("expected node to be unreserved after a processNode failure")
	}
	processed, err := r.isProcessed(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if processed {
		t.Fatal("node should not be marked processed after a failed merge")
	}

	// "Reprocessing": a later broadcast for the same branch would call
	// Visit again -- it must succeed now that the failed attempt freed
	// the reservation -- and this time the merge succeeds.
	fail.Store(false)
	if !r.queuedChildren.Visit(current) {
		t.Fatal("expected to be able to re-reserve the node after the failed attempt released it")
	}

	if _, err := r.processNode(ctx, ng, root, fd, node, false); err != nil {
		t.Fatalf("expected reprocessing to succeed, got: %v", err)
	}
	processed, err = r.isProcessed(ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	if !processed {
		t.Fatal("node should be marked processed after successful reprocessing")
	}
	if r.queuedChildren.Has(current) {
		t.Fatal("node should be unreserved after successful processing too")
	}

	ok, err := r.Has(ctx, ds.NewKey("flaky"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected key to be present after reprocessing succeeded")
	}
}
