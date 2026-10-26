package kvdb

import (
	"context"
	"testing"
	"testing/synctest"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// fetchedGetter answers GetMany with nodes it already holds.
type fetchedGetter struct {
	ipld.NodeGetter
	nodes []ipld.Node
}

func (g *fetchedGetter) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(g.nodes))
	for _, nd := range g.nodes {
		out <- &ipld.NodeOption{Node: nd}
	}
	close(out)
	return out
}

// TestGetDeltasKeepsWhatWasFetchedInTime: a walk reads a delta, queues it
// for a worker, and reads the next. The channel held one delta, so while the
// walk waited on its workers the next was held back, and once the fetch's
// context expired it was dropped: a block fetched in time was recorded as
// missing and left to the next repair. Under load TestCRDTPriority waited a
// minute for that repair.
func TestGetDeltasKeepsWhatWasFetchedInTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &fetchedGetter{}
		var cids []cid.Cid
		for i := range 3 {
			nd, err := makeNode(&pb.Delta{Priority: uint64(i + 1)}, nil)
			if err != nil {
				t.Fatal(err)
			}
			g.nodes = append(g.nodes, nd)
			cids = append(cids, nd.Cid())
		}

		ctx, cancel := context.WithCancel(t.Context())
		deltas := (&crdtNodeGetter{NodeGetter: g}).GetDeltas(ctx, cids)
		got := 0
		if opt := <-deltas; opt.err != nil {
			t.Fatal(opt.err)
		}
		got++
		// The walk is queueing the first for a worker when the fetch's
		// time runs out.
		synctest.Wait()
		cancel()
		synctest.Wait()
		for opt := range deltas {
			if opt.err != nil {
				t.Fatal(opt.err)
			}
			got++
		}
		if got != len(cids) {
			t.Fatalf("got %d of the %d deltas fetched", got, len(cids))
		}
	})
}
