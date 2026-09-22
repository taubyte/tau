package distributed

import (
	"context"
	"testing"
	"time"

	"github.com/samyfodil/mushql/crdt"
	_ "github.com/samyfodil/mushql/puredriver"
)

// ghostOp is a well-formed op from a third site that this process never runs.
func ghostOp(syncer *crdt.Syncer, seq uint64, id string) crdt.Op {
	return crdt.Op{
		Site: "ghost",
		Seq:  seq,
		HLC:  syncer.Store().Clock().Now(),
		Tbl:  "entries",
		PK:   crdt.EncodePK(id),
		Kind: crdt.OpInsert,
		Cells: []crdt.Cell{
			crdt.CellFromValue("payee", "ghost"),
			crdt.CellFromValue("amount", int64(1)),
		},
	}
}

// TestARowWhoseOpSeqIsNotContiguousNeverReachesASecondPeer is the consequence
// the test above measures the cause of.
//
// mushql's version vector is the whole of its anti-entropy: ExchangeVV is what
// a peer is offered and GetOps is what it asks for. Store.advanceVV walks
// CONTIGUOUSLY from the seq it already has, so one missing seq pins a site's
// entry at its current value forever, however many later ops arrive. The row
// materializes on the node that received the op and is then invisible to every
// node behind it.
//
// Which matters here because the capture path demonstrably produces those
// gaps: see TestConcurrentWritesThroughACapturedHandleAreAllCaptured, where
// sixteen writes occupied a seq range of seventy.
func TestARowWhoseOpSeqIsNotContiguousNeverReachesASecondPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	relay, relayDB := wrapLedger(t, t.Name()+"-relay")
	puller, pullerDB := wrapLedger(t, t.Name()+"-puller")

	net := crdt.NewMemNetwork()
	relayNode := relay.Attach(net.AddNode(crdt.PeerID(relay.Site()), relay.OpSource()))
	pullerNode := puller.Attach(net.AddNode(crdt.PeerID(puller.Site()), puller.OpSource()))

	go relayNode.Run(ctx, 20*time.Millisecond)
	go pullerNode.Run(ctx, 20*time.Millisecond)

	// The relay receives a third site's op whose seq is 2. Nothing is wrong
	// with the op; seq 1 simply never existed, which is what a retried commit
	// leaves behind.
	if err := relay.Store().Ingest(ctx, ghostOp(relay, 2, "g1"), false); err != nil {
		t.Fatalf("the relay ingesting the op: %v", err)
	}

	// The relay holds the row.
	if got := entriesOn(t, relayDB); got["g1"] != 1 {
		t.Fatalf("the relay does not hold the row it ingested: %v", got)
	}

	if seq := relay.Store().LocalVV()["ghost"]; seq != 0 {
		t.Skipf("mushql now advances the version vector past a gap (ghost is at %d); this test describes behaviour it no longer has — delete it", seq)
	}

	// Give anti-entropy several rounds. Nothing can arrive: the relay offers a
	// version vector that says it has nothing from ghost at all.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := entriesOn(t, pullerDB); got["g1"] == 1 {
			t.Fatal("the row reached the second peer; mushql no longer strands it — delete this test")
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Log("the relay holds a row that no peer can ever pull: its version vector reports nothing from that site")
}

// TestAGapStopsEveryLaterOpOfThatSiteToo is the same defect at the scale that
// makes it permanent rather than merely lossy: the gap is at seq 1 and a
// hundred good ops sit behind it. Anti-entropy moves none of them.
func TestAGapStopsEveryLaterOpOfThatSiteToo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	relay, _ := wrapLedger(t, t.Name()+"-relay")
	puller, pullerDB := wrapLedger(t, t.Name()+"-puller")

	net := crdt.NewMemNetwork()
	relayNode := relay.Attach(net.AddNode(crdt.PeerID(relay.Site()), relay.OpSource()))
	pullerNode := puller.Attach(net.AddNode(crdt.PeerID(puller.Site()), puller.OpSource()))

	go relayNode.Run(ctx, 20*time.Millisecond)
	go pullerNode.Run(ctx, 20*time.Millisecond)

	const rows = 100
	for i := 0; i < rows; i++ {
		// Seqs 2..101: contiguous among themselves, and all behind the one
		// seq that never existed.
		if err := relay.Store().Ingest(ctx, ghostOp(relay, uint64(i+2), pad(i)), false); err != nil {
			t.Fatalf("the relay ingesting op %d: %v", i+2, err)
		}
	}

	if seq := relay.Store().LocalVV()["ghost"]; seq != 0 {
		t.Skipf("mushql now advances the version vector past a gap (ghost is at %d) — delete this test", seq)
	}

	time.Sleep(500 * time.Millisecond)
	if got := len(entriesOn(t, pullerDB)); got != 0 {
		t.Fatalf("%d of %d rows crossed the gap; mushql no longer strands them — delete this test", got, rows)
	}
	t.Logf("%d rows held by the relay, 0 reachable by anti-entropy", rows)
}

func pad(i int) string {
	const digits = "0123456789"
	return "g" + string([]byte{digits[(i/10)%10], digits[i%10]})
}
