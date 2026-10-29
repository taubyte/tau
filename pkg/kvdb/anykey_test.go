package kvdb

import (
	"bytes"
	"context"
	"testing"
	"time"

	bsnet "github.com/ipfs/boxo/bitswap/network/bsnet"
	ds "github.com/ipfs/go-datastore"
	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/keypair"
	taupeer "github.com/taubyte/tau/p2p/peer"
)

// anyKey is a key that is not UTF-8: its bytes are the key, whatever they
// spell.
var anyKey = ds.RawKey("/k\xff\xfe\x00\xc3\x28")

func mustGet(t testing.TB, r *Datastore, k ds.Key, want []byte) {
	t.Helper()
	got, err := r.Get(context.Background(), k)
	if err != nil {
		t.Fatalf("%q: %s", k.String(), err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%q = %q, want %q", k.String(), got, want)
	}
}

// TestAKeyOfAnyBytesRoundTrips: a key is bytes. One that is not UTF-8 is
// written, merged as a delta, compacted, merged from a snapshot, caught up
// over the network and removed like any other. A delta's keys were protobuf
// strings, and the record of a replica's own write, encoded with a UTF-8
// check, refused such a key once the replica had heard its fleet.
func TestAKeyOfAnyBytesRoundTrips(t *testing.T) {
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 3, noAutoCompact())
	defer done()
	a, b, c := replicas[0], replicas[1], replicas[2]
	from(dagsyncs, 0, 1)
	put(t, b, "/fleet", "b")
	syncReplicaHeads(t, b, a)

	value := []byte("any\xffvalue")
	if err := a.Put(ctx, anyKey, value); err != nil {
		t.Fatal(err)
	}
	mustGet(t, a, anyKey, value)
	syncReplicaHeads(t, a, b)
	mustGet(t, b, anyKey, value)

	compact(t, a)
	mustGet(t, a, anyKey, value)
	assertTreesMatch(t, a)

	syncReplicaHeads(t, a, c)
	if c.IsDirty(ctx) {
		t.Fatal("the snapshot merge did not finish")
	}
	mustGet(t, c, anyKey, value)
	assertTreesMatch(t, c)
	if got := queryAll(t, c)[anyKey.String()]; !bytes.Equal(got, value) {
		t.Fatalf("a query of c gives %q under the key, want %q", got, value)
	}

	lag := netReplica(t, "lag")
	src := netReplica(t, "src")
	lag.connect(t, src)
	if err := src.r.Put(ctx, anyKey, value); err != nil {
		t.Fatal(err)
	}
	if _, err := src.r.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	lag.catchUp(t, src)
	mustGet(t, lag.r, anyKey, value)
	assertTreesMatch(t, lag.r)

	if err := c.Delete(ctx, anyKey); err != nil {
		t.Fatal(err)
	}
	from(dagsyncs, 0, 2)
	syncReplicaHeads(t, c, a)
	if ok, err := a.Has(ctx, anyKey); err != nil || ok {
		t.Fatalf("a still holds the key removed on c (%v, %v)", ok, err)
	}
}

// A netPeer is a replica on a libp2p node of its own over loopback TCP,
// every block it lacks fetched through bitswap.
type netPeer struct {
	r    *Datastore
	node taupeer.Node
}

func netReplica(t *testing.T, name string) *netPeer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	node, err := taupeer.New(ctx, t.TempDir(), keypair.NewRaw(), nil, []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, false)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		node.Close()
		cancel()
	})
	if err := node.WaitForSwarm(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	o := noAutoCompact()
	o.DAGSyncerTimeout = 10 * time.Second
	r, err := NewDatastore(newSortedStore(), ds.NewKey("anykey"), node.DAG(), &nullBroadcaster{}, replicaOptions(o, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return &netPeer{r: r, node: node}
}

// connect returns once identify has told n that peer speaks bitswap.
func (n *netPeer) connect(t *testing.T, peer *netPeer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := n.node.Peer().Connect(ctx, peercore.AddrInfo{ID: peer.node.ID(), Addrs: peer.node.Peer().Addrs()}); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		speaks, err := n.node.Peer().Peerstore().SupportsProtocols(peer.node.ID(), bsnet.ProtocolBitswap)
		if err != nil {
			t.Fatal(err)
		}
		if len(speaks) > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("identify never told the lagging node that its peer speaks bitswap")
		case <-tick.C:
		}
	}
}

// catchUp walks peer's heads, then repairs until nothing is missing.
func (n *netPeer) catchUp(t *testing.T, peer *netPeer) {
	t.Helper()
	ctx := context.Background()
	hs, _ := peer.r.heads.List()
	for _, h := range hs {
		if err := n.r.handleBlock(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if !n.r.IsDirty(ctx) {
			return
		}
		if err := n.r.Repair(ctx); err != nil {
			t.Log(err)
		}
	}
	if n.r.IsDirty(ctx) {
		t.Fatal("the catch-up did not finish")
	}
}
