package kvdb

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

func TestStaleSnapshotAfterTwoGenerationsDoesNotResurrect(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 3, nil)
	defer closeReplicas()
	a, b, f := replicas[0], replicas[1], replicas[2]
	ctx := context.Background()
	k := ds.NewKey("x")

	if err := a.Put(ctx, k, []byte("X")); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, ds.NewKey("bystander"), []byte("y")); err != nil {
		t.Fatal(err)
	}
	dagsyncs[1].remote = dagsyncs[0].DAGService
	syncReplicaHeads(t, a, b, "")

	if err := a.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, ds.NewKey("bystander2"), []byte("z")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	if _, err := b.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	syncReplicaHeads(t, a, b, "")
	if has, _ := b.Has(ctx, k); has {
		t.Errorf("offline replica kept the deleted key after receiving the second snapshot")
	}

	dagsyncs[2].remote = dagsyncs[0].DAGService
	syncReplicaHeads(t, a, f, "")
	dagsyncs[2].remote = dagsyncs[1].DAGService
	syncReplicaHeads(t, b, f, "")
	if has, _ := f.Has(ctx, k); has {
		t.Errorf("fresh replica resurrected the deleted key from a stale snapshot")
	}
	if has, _ := a.Has(ctx, k); has {
		t.Errorf("compactor resurrected")
	}
}
