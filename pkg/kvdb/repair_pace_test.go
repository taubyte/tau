package kvdb

import (
	"context"
	"fmt"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
)

// slowGetDAG makes every block read take a known time, standing in for a
// cold read of old history.
type slowGetDAG struct {
	*mockDAGSvc
	delay time.Duration
}

func (s *slowGetDAG) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	time.Sleep(s.delay)
	return s.mockDAGSvc.Get(ctx, c)
}

// The repair walk must leave most of the machine to foreground work: it idles
// repairIdleFactor times as long as it works, whatever the DAG size.
func TestRepairDAGIsPaced(t *testing.T) {
	const nodes, delay = 40, 5 * time.Millisecond
	d, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("pace"), &slowGetDAG{newTestDagsync(), delay}, nil, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	ctx := context.Background()
	for i := range nodes {
		if err := d.Put(ctx, ds.NewKey(fmt.Sprint(i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	d.MarkDirty(ctx)
	start := time.Now()
	if err := d.Repair(ctx); err != nil {
		t.Fatal(err)
	}
	// Only a lower bound: a slower machine only makes this easier to pass.
	if got, want := time.Since(start), (nodes-1)*delay*repairIdleFactor; got < want {
		t.Fatalf("repair of %d nodes took %s, want at least %s", nodes, got, want)
	}
	if d.IsDirty(ctx) {
		t.Fatal("a finished repair must mark the store clean")
	}
}
