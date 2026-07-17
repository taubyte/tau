package kvdb

import (
	"bytes"
	"context"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	"github.com/multiformats/go-multihash"
)

var (
	headsTestNS     = ds.NewKey("headstest")
	headsTestDagsNS = ds.NewKey("headstestdags")
)

var randg = rand.New(rand.NewSource(time.Now().UnixNano()))

// TODO we should also test with a non-batching store
func newTestHeads(t *testing.T) *heads {
	t.Helper()
	ctx := context.Background()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	heads, err := newHeads(ctx, store, headsTestNS, headsTestDagsNS, &testLogger{
		name: t.Name(),
		l:    DefaultOptions().Logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	return heads
}

func newCID(t *testing.T) cid.Cid {
	t.Helper()
	var buf [32]byte
	_, _ = randg.Read(buf[:])

	mh, err := multihash.Sum(buf[:], multihash.SHA2_256, -1)
	if err != nil {
		t.Fatal(err)
	}
	return cid.NewCidV1(cid.DagProtobuf, mh)
}

func TestHeadsBasic(t *testing.T) {
	ctx := context.Background()

	heads := newTestHeads(t)
	l, err := heads.Len(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if l != 0 {
		t.Errorf("new heads should have Len==0, got: %d", l)
	}

	cidHeights := make(map[cid.Cid]Head)
	numHeads := 5
	for range numHeads {
		c, height := newCID(t), uint64(randg.Int())
		head := Head{Cid: c}
		head.Height = height
		cidHeights[c] = head

		err := heads.Add(ctx, head)
		if err != nil {
			t.Fatal(err)
		}
	}

	assertHeads(t, heads, cidHeights)

	for c, old := range cidHeights {
		newC, newHeight := newCID(t), uint64(randg.Int())
		head := Head{Cid: newC}
		head.Height = newHeight
		err := heads.Replace(ctx, old, head)
		if err != nil {
			t.Fatal(err)
		}
		delete(cidHeights, c)
		cidHeights[newC] = head
		assertHeads(t, heads, cidHeights)
	}

	// Now try creating a new heads object and make sure what we
	// stored before is still there.
	err = heads.store.Sync(ctx, headsTestNS)
	if err != nil {
		t.Fatal(err)
	}

	heads, err = newHeads(ctx, heads.store, headsTestNS, headsTestDagsNS, &testLogger{
		name: t.Name(),
		l:    DefaultOptions().Logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHeads(t, heads, cidHeights)
}

func TestHeadsListDAG(t *testing.T) {
	ctx := context.Background()

	heads := newTestHeads(t)

	// Add heads with different DAG names
	dag1Heads := make(map[cid.Cid]Head)
	dag2Heads := make(map[cid.Cid]Head)

	// Add 3 heads for DAG 1
	for range 3 {
		c, height := newCID(t), uint64(randg.Intn(100))
		head := Head{Cid: c, HeadValue: HeadValue{Height: height, DAGName: "dag1"}}
		dag1Heads[c] = head
		err := heads.Add(ctx, head)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Add 2 heads for DAG 2
	for range 2 {
		c, height := newCID(t), uint64(randg.Intn(100))
		head := Head{Cid: c, HeadValue: HeadValue{Height: height, DAGName: "dag2"}}
		dag2Heads[c] = head
		err := heads.Add(ctx, head)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Test that ListDAG("dag1") returns only DAG 1 heads
	dag1HeadsList, maxHeight1, err := heads.ListDAG(ctx, "dag1")
	if err != nil {
		t.Fatal(err)
	}

	if len(dag1HeadsList) != 3 {
		t.Errorf("expected 3 heads for DAG 1, got %d", len(dag1HeadsList))
	}

	if maxHeight1 == 0 {
		t.Error("expected max height > 0 for DAG 1")
	}

	// Verify all returned heads belong to DAG 1
	for _, head := range dag1HeadsList {
		if head.DAGName != "dag1" {
			t.Errorf("head %s belongs to DAG %s, expected DAG 1", head.Cid, head.DAGName)
		}
	}

	// Test that ListDAG("dag2") returns only DAG 2 heads
	dag2HeadsList, maxHeight2, err := heads.ListDAG(ctx, "dag2")
	if err != nil {
		t.Fatal(err)
	}

	if len(dag2HeadsList) != 2 {
		t.Errorf("expected 2 heads for DAG 2, got %d", len(dag2HeadsList))
	}

	if maxHeight2 == 0 {
		t.Error("expected max height > 0 for DAG 2")
	}

	// Verify all returned heads belong to DAG 2
	for _, head := range dag2HeadsList {
		if head.DAGName != "dag2" {
			t.Errorf("head %s belongs to DAG %s, expected DAG 2", head.Cid, head.DAGName)
		}
	}

	// Verify max heights are correct
	expectedMaxHeight1 := uint64(0)
	for _, head := range dag1Heads {
		if head.Height > expectedMaxHeight1 {
			expectedMaxHeight1 = head.Height
		}
	}

	expectedMaxHeight2 := uint64(0)
	for _, head := range dag2Heads {
		if head.Height > expectedMaxHeight2 {
			expectedMaxHeight2 = head.Height
		}
	}

	if maxHeight1 != expectedMaxHeight1 {
		t.Errorf("expected max height for DAG 1 %d, got %d", expectedMaxHeight1, maxHeight1)
	}

	if maxHeight2 != expectedMaxHeight2 {
		t.Errorf("expected max height for DAG 2 %d, got %d", expectedMaxHeight2, maxHeight2)
	}

	// Test that ListDAG with non-existent DAG returns empty list
	emptyHeads, maxHeightEmpty, err := heads.ListDAG(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}

	if len(emptyHeads) != 0 {
		t.Errorf("expected 0 heads for non-existent DAG, got %d", len(emptyHeads))
	}

	if maxHeightEmpty != 0 {
		t.Errorf("expected max height 0 for non-existent DAG, got %d", maxHeightEmpty)
	}
}

func assertHeads(t *testing.T, hh *heads, headsMap map[cid.Cid]Head) {
	t.Helper()
	ctx := context.Background()

	heads, maxHeight, err := hh.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var expectedMaxHeight uint64
	for _, head := range headsMap {
		if head.Height > expectedMaxHeight {
			expectedMaxHeight = head.Height
		}
	}
	if maxHeight != expectedMaxHeight {
		t.Errorf("expected max height=%d, got=%d", expectedMaxHeight, maxHeight)
	}

	headsLen, err := hh.Len(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != headsLen {
		t.Errorf("expected len and list to agree, got listLen=%d, len=%d", len(heads), headsLen)
	}

	mapcids := make([]cid.Cid, 0, len(headsMap))
	for c := range headsMap {
		mapcids = append(mapcids, c)
	}

	headcids := make([]cid.Cid, 0, len(headsMap))
	for _, h := range headsMap {
		headcids = append(headcids, h.Cid)
	}

	sort.Slice(mapcids, func(i, j int) bool {
		ci := mapcids[i].Bytes()
		cj := mapcids[j].Bytes()
		return bytes.Compare(ci, cj) < 0
	})

	sort.Slice(headcids, func(i, j int) bool {
		ci := headcids[i].Bytes()
		cj := headcids[j].Bytes()
		return bytes.Compare(ci, cj) < 0
	})

	if !reflect.DeepEqual(mapcids, headcids) {
		t.Errorf("given cids don't match cids returned by List: %v, %v", mapcids, headcids)
	}
	for _, c := range mapcids {
		head, ok := hh.Get(ctx, c)
		if !ok {
			t.Errorf("cid returned by List reported absent by IsHead: %v", c)
		}
		if head.Height != headsMap[head.Cid].Height {
			t.Errorf("expected cid %v to have height %d, got: %d", c, headsMap[c].Height, head.Height)
		}
	}
}
