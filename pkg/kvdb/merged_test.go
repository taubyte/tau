package kvdb

import (
	"testing"

	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
)

// A REPLICA COVERS WHAT ANOTHER MERGED ONLY ONCE IT HAS MERGED IT ALL: a replica that
// has not heard a write does not cover it, one that has does, and covering is not
// symmetric.
func TestAReplicaCoversWhatAnotherMergedOnceItHasMergedIt(t *testing.T) {
	aDAG, bDAG := newFetchThrough(), newFetchThrough()
	aDAG.setRemotes(bDAG.DAGService)
	bDAG.setRemotes(aDAG.DAGService)
	a := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), aDAG, "covers a: ")
	b := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), bDAG, "covers b: ")

	put(t, a, "/one", "1")
	put(t, a, "/two", "2")
	if b.Covers(a.Merged()) {
		t.Fatal("a replica that heard nothing covered another's writes")
	}
	if !a.Covers(a.Merged()) {
		t.Fatal("a replica does not cover what it merged itself")
	}

	syncReplicaHeads(t, a, b)
	if !b.Covers(a.Merged()) {
		t.Fatalf("a replica that merged another's heads does not cover them: %v against %v", b.Merged(), a.Merged())
	}

	put(t, b, "/three", "3")
	if a.Covers(b.Merged()) {
		t.Fatal("a replica covered a write it never heard")
	}
	if !b.Covers(a.Merged()) {
		t.Fatal("writing more lost what the replica covered")
	}
}

func TestASpanIsCoveredWholeOrNotAtAll(t *testing.T) {
	c := newCausalContext()
	c.inc["r"] = &known{spans: []span{{lo: 0, hi: 4}, {lo: 6, hi: 9}}}

	for _, tc := range []struct {
		s    Span
		want bool
	}{
		{Span{Replica: "r", Lo: 0, Hi: 4}, true},
		{Span{Replica: "r", Lo: 1, Hi: 3}, true},
		{Span{Replica: "r", Lo: 6, Hi: 9}, true},
		{Span{Replica: "r", Lo: 3, Hi: 7}, false},
		{Span{Replica: "r", Lo: 0, Hi: 9}, false},
		{Span{Replica: "r", Lo: 8, Hi: 10}, false},
		{Span{Replica: "other", Lo: 0, Hi: 1}, false},
		{Span{Replica: "other", Lo: 3, Hi: 3}, true},
	} {
		if got := c.coversSpan(tc.s); got != tc.want {
			t.Errorf("coversSpan(%+v) = %v, want %v", tc.s, got, tc.want)
		}
	}
	if got := c.spans(); len(got) != 2 || got[0] != (Span{"r", 0, 4}) || got[1] != (Span{"r", 6, 9}) {
		t.Errorf("spans() = %v", got)
	}
}
