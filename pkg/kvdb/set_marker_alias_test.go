package kvdb

// Round-3 tests for the stable-element-id / marker-alias plumbing (SPEC3
// S1-S6): encodeMarker/decodeMarker (S1), putElems preserving original
// element ids for snapshot deltas (S2), findBestValue and purgeKeyBlocks
// becoming alias-aware (S3/S4), compactSnapshotState scoping/fetching via
// alias-or-path and emitting original ids (S5), and the coordination-free
// Compact/reclaim contract end-to-end (S6). The dedicated S8 test suite
// (broader edge-case coverage) lands in the next stage; these are the
// direct tests that pinned the S1-S6 behavior while implementing it.

import (
	"bytes"
	"context"
	"testing"

	dshelp "github.com/ipfs/boxo/datastore/dshelp"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	dssync "github.com/ipfs/go-datastore/sync"
	"github.com/multiformats/go-multihash"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// fakeBlockCid deterministically derives a CID from seed, for tests that
// need distinct, stable, but otherwise meaningless block identifiers.
func fakeBlockCid(t testing.TB, seed string) cid.Cid {
	t.Helper()
	mh, err := multihash.Sum([]byte(seed), multihash.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	return cid.NewCidV1(cid.DagProtobuf, mh)
}

// TestMarkerAliasCodec unit-tests encodeMarker/decodeMarker (S1) directly:
// round-tripping with and without an alias, decoding a legacy bare-varint
// marker (pre-alias format, still a valid encodeMarker output with an Undef
// alias), and the malformed-input error branches.
func TestMarkerAliasCodec(t *testing.T) {
	alias := fakeBlockCid(t, "alias-block")

	cases := []struct {
		name  string
		prio  uint64
		alias cid.Cid
	}{
		{"no alias, zero priority", 0, cid.Undef},
		{"no alias", 42, cid.Undef},
		{"with alias", 7, alias},
		{"large priority with alias", 1 << 40, alias},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := encodeMarker(tc.prio, tc.alias)
			gotPrio, gotAlias, err := decodeMarker(buf)
			if err != nil {
				t.Fatalf("decodeMarker failed: %s", err)
			}
			if gotPrio != tc.prio {
				t.Fatalf("priority round-trip mismatch: put %d, got %d", tc.prio, gotPrio)
			}
			if tc.alias.Defined() {
				if !gotAlias.Equals(tc.alias) {
					t.Fatalf("alias round-trip mismatch: put %s, got %s", tc.alias, gotAlias)
				}
			} else if gotAlias.Defined() {
				t.Fatalf("expected no alias, got %s", gotAlias)
			}
		})
	}

	// A legacy bare-varint marker (exactly what encodePriority alone
	// produces, and what every marker written before this format existed
	// looks like) must decode via decodeMarker identically to
	// encodeMarker(prio, cid.Undef): this is what makes the new format
	// purely additive, no storage migration required.
	legacy := encodePriority(9)
	if bytes.Compare(legacy, encodeMarker(9, cid.Undef)) != 0 {
		t.Fatalf("encodeMarker(prio, Undef) should be byte-identical to encodePriority(prio)")
	}
	gotPrio, gotAlias, err := decodeMarker(legacy)
	if err != nil {
		t.Fatalf("decodeMarker(legacy) failed: %s", err)
	}
	if gotPrio != 9 || gotAlias.Defined() {
		t.Fatalf("legacy bare-varint marker decode mismatch: prio=%d alias=%s", gotPrio, gotAlias)
	}

	// Malformed input error branches.
	if _, _, err := decodeMarker(nil); err == nil {
		t.Fatal("expected an error decoding a nil marker")
	}
	if _, _, err := decodeMarker([]byte{}); err == nil {
		t.Fatal("expected an error decoding an empty marker")
	}
	bad := append(encodePriority(3), 0xff, 0xff) // not a valid CID remainder
	if _, _, err := decodeMarker(bad); err == nil {
		t.Fatal("expected an error decoding a marker with malformed alias bytes")
	}
}

// aliasesUnderPrefix returns the decoded alias (only non-Undef ones) of
// every marker under prefix.
func aliasesUnderPrefix(t testing.TB, r *Datastore, prefix ds.Key) []cid.Cid {
	t.Helper()
	ctx := context.Background()
	res, err := r.store.Query(ctx, query.Query{Prefix: prefix.String(), KeysOnly: false})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close() //nolint:errcheck
	var out []cid.Cid
	for e := range res.Next() {
		if e.Error != nil {
			t.Fatal(e.Error)
		}
		if len(e.Value) == 0 {
			continue
		}
		_, alias, err := decodeMarker(e.Value)
		if err != nil {
			t.Fatal(err)
		}
		if alias.Defined() {
			out = append(out, alias)
		}
	}
	return out
}

// markerAliasesForKey returns the decoded alias (only non-Undef ones) of
// every surviving element marker under key.
func markerAliasesForKey(t testing.TB, r *Datastore, key string) []cid.Cid {
	t.Helper()
	return aliasesUnderPrefix(t, r, r.set.elemsPrefix(key))
}

// tombAliasesForKey returns the decoded alias (only non-Undef ones) of every
// surviving tombstone marker under key (see putTombs' carried-tombstone
// aliasing).
func tombAliasesForKey(t testing.TB, r *Datastore, key string) []cid.Cid {
	t.Helper()
	return aliasesUnderPrefix(t, r, r.set.tombsPrefix(key))
}

// TestPurgeKeepsAliasedMarkers directly unit-tests purgeKeyBlocks's S4 keep
// rule: a marker is deleted when its own path-id CID is in the purge set
// AND (it has no alias OR its alias is also in the purge set); it is kept
// otherwise, even though its path-id CID is being purged, because the
// element it names is still hosted by a live (non-purged) block.
func TestPurgeKeepsAliasedMarkers(t *testing.T) {
	// MutexWrap: newTestDatastore's Datastore runs a background repair
	// worker that touches the underlying store concurrently with this
	// test's direct writes below; a bare MapDatastore is not safe for
	// that (see makeStore's mapStore case, which wraps identically).
	d := newTestDatastore(t, dssync.MutexWrap(ds.NewMapDatastore()))
	ctx := context.Background()

	// A real "snapshot" block that hosts the aliased element's delta, so
	// that purgeKeyBlocks' final findBestValue recompute can genuinely
	// fetch it.
	snapDelta := d.newDelta()
	snapDelta.SetDagName("")
	snapDelta.SetSnapshot(true)

	oldDelta := d.newDelta()
	oldNode, err := d.putBlock(ctx, nil, oldDelta)
	if err != nil {
		t.Fatal(err)
	}
	oldCid := oldNode.Cid()
	oldID := dsIDFromCid(oldCid)

	otherOldDelta := d.newDelta()
	otherOldDelta.SetPriority(1)
	otherOldNode, err := d.putBlock(ctx, nil, otherOldDelta)
	if err != nil {
		t.Fatal(err)
	}
	otherOldCid := otherOldNode.Cid()
	otherOldID := dsIDFromCid(otherOldCid)

	snapDelta.SetElements([]*pb.Element{
		{Key: "/k", Id: oldID, Value: []byte("v-snap"), Priority: 5},
	})
	snapNode, err := d.putBlock(ctx, nil, snapDelta)
	if err != nil {
		t.Fatal(err)
	}
	snapCid := snapNode.Cid()

	// purgedButAliased: its own path-id CID (oldCid) will be purged, but
	// its alias (snapCid) is NOT in the purge set -- must survive.
	kSurvivor := d.set.elemsPrefix("/k").ChildString(oldID)
	if err := d.set.store.Put(ctx, kSurvivor, encodeMarker(5, snapCid)); err != nil {
		t.Fatal(err)
	}
	// purgedNoAlias: its own path-id CID (otherOldCid) will be purged and
	// it has no alias -- must be deleted.
	kGone := d.set.elemsPrefix("/k").ChildString(otherOldID)
	if err := d.set.store.Put(ctx, kGone, encodeMarker(1, cid.Undef)); err != nil {
		t.Fatal(err)
	}

	blockCIDs := map[cid.Cid]struct{}{oldCid: {}, otherOldCid: {}}
	if err := d.set.purgeKeyBlocks(ctx, "/k", blockCIDs, true, false); err != nil {
		t.Fatal(err)
	}

	if has, err := d.set.store.Has(ctx, kSurvivor); err != nil || !has {
		t.Fatalf("expected the aliased marker to survive purge, has=%v err=%v", has, err)
	}
	if has, err := d.set.store.Has(ctx, kGone); err != nil || has {
		t.Fatalf("expected the alias-less purged marker to be deleted, has=%v err=%v", has, err)
	}

	// findBestValue's recompute (run at the end of purgeKeyBlocks) should
	// have resolved the surviving aliased marker's value via its alias.
	v, err := d.set.Element(ctx, "/k")
	if err != nil {
		t.Fatal(err)
	}
	if string(v) != "v-snap" {
		t.Fatalf("expected recomputed value %q, got %q", "v-snap", v)
	}
}

// dsIDFromCid mirrors the blockKey computation processNode uses
// (dshelp.MultihashToDsKey(cid.Hash()).String()), for tests constructing
// marker ids directly from a CID.
func dsIDFromCid(c cid.Cid) string {
	return dshelp.MultihashToDsKey(c.Hash()).String()
}

// TestSnapshotElementKilledByLateTombstone pins the coordination-free
// contract from both temporal directions: a snapshot element must die when
// its original id's tombstone arrives, whether the tombstone arrives AFTER
// the snapshot (the element is live, then killed) or BEFORE it (the
// snapshot element is dead on arrival, via setValue's inTombsKeyID check).
func TestSnapshotElementKilledByLateTombstone(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 4, nil)
	defer closeReplicas()
	a, b, c, e := replicas[0], replicas[1], replicas[2], replicas[3]
	ctx := context.Background()

	k := ds.NewKey("contested")
	if err := a.Put(ctx, k, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	syncReplicaHeads(t, a, b, "")

	// Partitioned, logically-concurrent operations: b deletes the key
	// (tombstoning the element's original id) while a compacts (folding
	// the still-alive-as-far-as-a-knows element into a snapshot, which
	// keeps that exact original id -- putElems/S2).
	if err := b.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// Case 1: a fresh replica (c) syncs ONLY the snapshot first (the
	// element is alive there), then later receives the concurrent delete
	// -- the element must die on arrival of the tombstone.
	syncReplicaHeads(t, a, c, "")
	if has, err := c.Has(ctx, k); err != nil || !has {
		t.Fatalf("case1: expected key alive right after syncing the snapshot, has=%v err=%v", has, err)
	}
	dagsyncs[2].remote = dagsyncs[1].DAGService
	syncReplicaHeads(t, b, c, "")
	if has, err := c.Has(ctx, k); err != nil || has {
		t.Fatalf("case1: expected key dead after the late tombstone arrived, has=%v err=%v", has, err)
	}

	// Case 2 (mirror): a fresh replica (e) has the delete FIRST, then the
	// snapshot arrives later -- the snapshot element must be dead on
	// arrival (setValue's inTombsKeyID check rejects it outright).
	dagsyncs[3].remote = dagsyncs[1].DAGService
	syncReplicaHeads(t, b, e, "")
	if has, err := e.Has(ctx, k); err != nil || has {
		t.Fatalf("case2: expected key already dead after syncing the delete, has=%v err=%v", has, err)
	}
	dagsyncs[3].remote = dagsyncs[0].DAGService
	syncReplicaHeads(t, a, e, "")
	if has, err := e.Has(ctx, k); err != nil || has {
		t.Fatalf("case2: expected key to remain dead on arrival of the snapshot, has=%v err=%v", has, err)
	}
}

// TestSecondGenerationCompaction pins S5's second-generation scoping rule:
// after a first compaction re-homes elements under aliases, a SECOND
// compaction must still find them (scoping via alias, not just path id),
// re-alias the survivors to the new (gen2) snapshot, correctly resolve a
// key overwritten between the two generations, purge the now-superseded
// gen1 snapshot block, and produce state identical to a fresh replica that
// syncs only the gen2 snapshot.
func TestSecondGenerationCompaction(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 2, nil)
	defer closeReplicas()
	a, fresh := replicas[0], replicas[1]
	ctx := context.Background()

	if err := a.Put(ctx, ds.NewKey("k1"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, ds.NewKey("k2"), []byte("v2-orig")); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	headsGen1, _, err := a.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(headsGen1) != 1 {
		t.Fatalf("expected a single gen1 snapshot head, got %d", len(headsGen1))
	}
	gen1Cid := headsGen1[0].Cid

	// New key introduced after gen1, plus an overwrite of a key gen1
	// already folded into its snapshot.
	if err := a.Put(ctx, ds.NewKey("k3"), []byte("v3")); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, ds.NewKey("k2"), []byte("v2-new")); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	headsGen2, _, err := a.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(headsGen2) != 1 {
		t.Fatalf("expected a single gen2 snapshot head, got %d", len(headsGen2))
	}
	gen2Cid := headsGen2[0].Cid

	// Winner values are correct.
	want := map[string][]byte{"/k1": []byte("v1"), "/k2": []byte("v2-new"), "/k3": []byte("v3")}
	assertSameKV(t, want, queryAll(t, a))

	// Every key has EXACTLY ONE surviving marker, aliased to the gen2
	// snapshot: carried through unchanged (k1), re-pointed after winning an
	// overwrite race (k2), or aliased for the first time (k3). k2 is the
	// load-bearing case: its gen1 element (folded into the now-purged gen1
	// snapshot, then superseded by v2-new) must NOT leave a stale marker
	// aliased to that purged block -- purgeKeyBlocks deletes an element
	// marker whose value-host block is being purged, even when its own path
	// id was already purged a generation earlier (so is absent from this
	// purge set). A leftover stale marker would fetch a purged block, and
	// could resurrect v2-orig once v2-new is tombstoned by a snapshot-only
	// replica.
	for _, key := range []string{"/k1", "/k2", "/k3"} {
		if n := countPrefix(t, a, a.set.elemsPrefix(key)); n != 1 {
			t.Fatalf("key %s: expected exactly 1 surviving element marker after gen2, got %d", key, n)
		}
		aliases := markerAliasesForKey(t, a, key)
		if len(aliases) != 1 || !aliases[0].Equals(gen2Cid) {
			t.Fatalf("key %s: expected exactly one marker aliased to the gen2 snapshot %s, got %v", key, gen2Cid, aliases)
		}
	}

	// The superseded gen1 snapshot block is gone.
	if _, err := dagsyncs[0].DAGService.Get(ctx, gen1Cid); err == nil {
		t.Fatalf("gen1 snapshot block %s should have been purged by gen2's compaction", gen1Cid)
	}

	// A fresh replica syncing only gen2 converges to identical state.
	syncReplicaHeads(t, a, fresh, "")
	assertSameKV(t, queryAll(t, a), queryAll(t, fresh))
}

// TestSecondGenerationOverwriteDeleteConverges is the coordination-free
// regression for the stale-loser-marker hazard: a key overwritten BETWEEN two
// compaction generations must not let the compactor retain a stale, purged
// element marker for the superseded version. If it does, a snapshot-only
// replica that deletes the (single) winner it knows about tombstones only the
// winner's id -- and the compactor, still holding the superseded version's
// marker (which the delete never targeted), would either fetch its purged
// host block (a merge error) or resurrect the stale lower-priority value,
// diverging from the deleter. Both replicas must instead converge on the key
// being DELETED, exactly as an uncompacted history would.
func TestSecondGenerationOverwriteDeleteConverges(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 2, nil)
	defer closeReplicas()
	a, snapOnly := replicas[0], replicas[1]
	ctx := context.Background()

	k := ds.NewKey("k")
	if err := a.Put(ctx, k, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Compact(ctx, ""); err != nil { // gen1: folds v1 into snapshot S1.
		t.Fatal(err)
	}
	if err := a.Put(ctx, k, []byte("v2")); err != nil { // overwrite: v2 supersedes v1.
		t.Fatal(err)
	}
	if _, err := a.Compact(ctx, ""); err != nil { // gen2: winner v2 re-homed to S2, S1 purged.
		t.Fatal(err)
	}

	// snapOnly learns k only through gen2's snapshot: it knows a single
	// element id for k (v2's), so its Delete tombstones only that id.
	syncReplicaHeads(t, a, snapOnly, "")
	if v, err := snapOnly.Get(ctx, k); err != nil || string(v) != "v2" {
		t.Fatalf("snapshot-only replica: expected v2, got %q err=%v", v, err)
	}
	dagsyncs[0].remote = dagsyncs[1].DAGService // a can fetch snapOnly's delete block.
	if err := snapOnly.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}

	// The compactor merges the delete. It must apply cleanly (no fetch of a
	// purged block) and leave the key deleted, converging with the deleter.
	syncReplicaHeads(t, snapOnly, a, "")

	for name, r := range map[string]*Datastore{"compactor": a, "deleter": snapOnly} {
		has, err := r.Has(ctx, k)
		if err != nil {
			t.Fatalf("%s: unexpected error checking k: %s", name, err)
		}
		if has {
			v, _ := r.Get(ctx, k)
			t.Fatalf("%s: expected k deleted after the winner-only delete, got %q", name, v)
		}
	}
	assertSameKV(t, queryAll(t, a), queryAll(t, snapOnly))
}

// TestCompactDivergentViewsConverge is the S6/S8 divergent-view convergence
// test: A and B compact DIFFERENT views (B has extra ops -- a delete of a
// key A has already snapshotted, and an overwrite of another key -- that A
// never saw before compacting). After exchanging snapshots both ways, A and
// B must converge to exactly the state a non-compacting oracle replica
// (which merges the same raw, uncompacted operations) reaches.
func TestCompactDivergentViewsConverge(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 3, nil)
	defer closeReplicas()
	a, b, oracle := replicas[0], replicas[1], replicas[2]
	dagsyncs[0].remote = dagsyncs[1].DAGService // a can fetch b's blocks for the exchange
	ctx := context.Background()

	common := ds.NewKey("common")
	shared := ds.NewKey("shared")    // B deletes this while A snapshots it unaware.
	overwritten := ds.NewKey("over") // B overwrites this after the partition.

	if err := a.Put(ctx, common, []byte("c0")); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, shared, []byte("keep-me")); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, overwritten, []byte("old")); err != nil {
		t.Fatal(err)
	}

	syncReplicaHeads(t, a, b, "")
	syncReplicaHeads(t, a, oracle, "")

	// Partitioned: B does extra ops A never sees before A compacts.
	if err := b.Delete(ctx, shared); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, overwritten, []byte("new")); err != nil {
		t.Fatal(err)
	}

	// The oracle mirrors B's extra (raw, uncompacted) ops directly, so its
	// final state is reached purely by merging history -- never compacting.
	dagsyncs[2].remote = dagsyncs[1].DAGService
	syncReplicaHeads(t, b, oracle, "")

	// A compacts its own (smaller, unaware) view; B independently compacts
	// its own (bigger, divergent) view.
	if _, err := a.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// Exchange both directions.
	syncReplicaHeads(t, b, a, "")
	syncReplicaHeads(t, a, b, "")

	assertSameKV(t, queryAll(t, a), queryAll(t, b))
	assertSameKV(t, queryAll(t, a), queryAll(t, oracle))

	if has, err := a.Has(ctx, shared); err != nil || has {
		t.Fatalf("expected the shared key to stay deleted, has=%v err=%v", has, err)
	}
	v, err := a.Get(ctx, overwritten)
	if err != nil {
		t.Fatal(err)
	}
	if string(v) != "new" {
		t.Fatalf("expected overwritten key to resolve to %q, got %q", "new", v)
	}
	v, err = a.Get(ctx, common)
	if err != nil {
		t.Fatal(err)
	}
	if string(v) != "c0" {
		t.Fatalf("expected common key to survive unchanged, got %q", v)
	}
}
