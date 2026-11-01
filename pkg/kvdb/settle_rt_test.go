package kvdb

import (
	"context"
	"encoding/binary"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

type chanBroadcaster struct{ in chan []byte }

func (c *chanBroadcaster) Broadcast(context.Context, []byte) error { return nil }

func (c *chanBroadcaster) Next(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ErrNoMoreBroadcast
	case b := <-c.in:
		return b, nil
	}
}

// hangingDAG serves what its remotes hold and waits out the deadline on
// anything else, as a fetch from a gone peer does.
type hangingDAG struct{ *fetchThroughDAGSvc }

func (h *hangingDAG) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	nd, err := h.fetchThroughDAGSvc.Get(ctx, c)
	if err == nil {
		return nd, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (h *hangingDAG) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(cids))
	go func() {
		defer close(out)
		for _, c := range cids {
			nd, err := h.Get(ctx, c)
			out <- &ipld.NodeOption{Node: nd, Err: err}
		}
	}()
	return out
}

func TestARebaseDoesNotWaitOnAVanishedJudge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		pDag := newFetchThrough()
		p := openTestStore(t, "p", dssync.MutexWrap(ds.NewMapDatastore()), pDag, 0)
		t.Cleanup(func() { p.Close() })
		put(t, p, "/k", "v")

		// r has heard a fleet before, so it opens rejoining.
		ns := ds.NewKey("crdttest")
		rDs := dssync.MutexWrap(ds.NewMapDatastore())
		since := millis(time.Now())
		if err := rDs.Put(ctx, ns.ChildString(contextNs).ChildString(sinceKey), binary.AppendUvarint(nil, since)); err != nil {
			t.Fatal(err)
		}
		rDag := &hangingDAG{newFetchThrough()}
		rDag.setRemotes(pDag.DAGService)
		in := &chanBroadcaster{in: make(chan []byte, 3)}
		opts := replicaOptions(DefaultOptions(), "r: ")
		opts.RebroadcastInterval = time.Hour
		opts.DeliveryBound = opts.DAGSyncerTimeout + opts.RebroadcastInterval
		opts.RepairInterval = 0
		r, err := NewDatastore(rDs, ns, rDag, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		r.mu.Lock()
		rejoining := r.rejoining
		r.mu.Unlock()
		if !rejoining {
			t.Fatal("setup: r should open rejoining")
		}

		// The judge says rebase, naming a head nobody can serve, and is gone.
		now := millis(time.Now())
		gone := merkledag.NodeWithData([]byte("only the judge held this"))
		verdict, err := proto.Marshal(&pb.CRDTBroadcast{
			Since:       since,
			Now:         now,
			Incarnation: []byte("judge"),
			Verdict:     &pb.Verdict{To: []byte(r.currentIncarnation()), Rebase: true, Since: since},
			Heads:       []*pb.Head{{Cid: gone.Cid().Bytes(), Dot: dot{replica: "judge", counter: 1}.toPB(), At: now, Ttl: durationMillis(opts.Retention)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		hs, _ := p.heads.List()
		beat, err := p.encodeBroadcast(ctx, hs, true)
		if err != nil {
			t.Fatal(err)
		}
		// Its last heartbeat, sent before it went, names the same head.
		last, err := proto.Marshal(&pb.CRDTBroadcast{
			Since:       since,
			Now:         now,
			Incarnation: []byte("judge"),
			Heads:       []*pb.Head{{Cid: gone.Cid().Bytes(), Dot: dot{replica: "judge", counter: 1}.toPB(), At: now, Ttl: durationMillis(opts.Retention)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		in.in <- verdict
		in.in <- last
		in.in <- beat
		synctest.Wait()
		if got := valueOf(t, r, ds.NewKey("/k")); got != "v" {
			t.Fatalf("while a rebase waited on a vanished judge, what a live peer sent was not merged: /k = %s", got)
		}
	})
}

func TestARebaseWritesNoSnapshotBeforeItHasWalkedTheJudgesState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		ns := ds.NewKey("crdttest")
		rDs := dssync.MutexWrap(ds.NewMapDatastore())
		since := millis(time.Now())
		if err := rDs.Put(ctx, ns.ChildString(contextNs).ChildString(sinceKey), binary.AppendUvarint(nil, since)); err != nil {
			t.Fatal(err)
		}
		in := &chanBroadcaster{in: make(chan []byte, 1)}
		opts := replicaOptions(DefaultOptions(), "r: ")
		// A maintenance pass, and no gap, while a fetch waits out its
		// deadline.
		opts.RebroadcastInterval = opts.DAGSyncerTimeout * 2 / 3
		opts.RepairInterval = 0
		r, err := NewDatastore(rDs, ns, &hangingDAG{newFetchThrough()}, in, opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		put(t, r, "/only", "mine")

		now := millis(time.Now())
		gone := merkledag.NodeWithData([]byte("only the judge held this"))
		verdict, err := proto.Marshal(&pb.CRDTBroadcast{
			Since:       since,
			Now:         now,
			Incarnation: []byte("judge"),
			Verdict:     &pb.Verdict{To: []byte(r.currentIncarnation()), Rebase: true, Since: since},
			Heads:       []*pb.Head{{Cid: gone.Cid().Bytes(), Dot: dot{replica: "judge", counter: 1}.toPB(), At: now, Ttl: durationMillis(opts.Retention)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		in.in <- verdict
		synctest.Wait()
		if r.currentGeneration() != 1 {
			t.Fatal("setup: r should have rebased")
		}
		if got := valueOf(t, r, ds.NewKey("/only")); got != "mine" {
			t.Fatalf("while a rebase walks the judge's state, what the replica wrote is gone: /only = %s", got)
		}
		// A maintenance pass comes while the walk waits on the judge.
		time.Sleep(opts.RebroadcastInterval)
		synctest.Wait()
		if n := r.heads.Len(); n != 0 {
			t.Fatalf("a snapshot was written while the judge's state was walked (%d heads)", n)
		}
		time.Sleep(opts.DAGSyncerTimeout)
		synctest.Wait()
		if n := r.heads.Len(); n != 1 {
			t.Fatalf("no snapshot carries what the rebase put back once the walk ended (%d heads)", n)
		}
		if got := valueOf(t, r, ds.NewKey("/only")); got != "mine" {
			t.Fatalf("/only = %s after the rebase, want mine", got)
		}
	})
}
