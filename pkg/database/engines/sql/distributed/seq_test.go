package distributed

import (
	"context"
	"testing"

	"github.com/samyfodil/mushql/crdt"
	_ "github.com/samyfodil/mushql/puredriver"
)

// TestTheCaptureCounterAndTheNodeCounterCollide pins a silent data loss in
// mushql that a tau engine has to know about, because both halves of it are
// exported and documented as usable.
//
// crdt.Syncer keeps its own local sequence counter for the ops its capture
// hook writes, and crdt.Node keeps a second one for the ops a caller
// synthesizes with MakeOp. Attach builds the Node from the Store and never
// tells it about the Syncer's counter, so both are seeded from the same
// version vector and both hand out 1 next. The oplog's primary key is
// (site, seq) and Ingest dedups on it, so the second op is discarded with no
// error, no log line and no materialized row.
//
// What it means for tau: an engine may use the wrapped *sql.DB or Node.MakeOp
// / Node.LocalWrite, never both.
func TestTheCaptureCounterAndTheNodeCounterCollide(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	syncer, userDB := wrapLedger(t, t.Name())

	net := crdt.NewMemNetwork()
	node := syncer.Attach(net.AddNode(crdt.PeerID(syncer.Site()), syncer.OpSource()))

	// The capture path takes seq 1.
	if _, err := userDB.ExecContext(ctx, `INSERT INTO entries(id,payee,amount) VALUES('e1','alice',42)`); err != nil {
		t.Fatalf("writing through the captured handle: %v", err)
	}
	if got := syncer.Store().LocalVV()[syncer.Site()]; got != 1 {
		t.Fatalf("the capture wrote seq %d, expected 1 — the rest of this test reads on that", got)
	}

	// The Node path hands out seq 1 as well.
	op := node.MakeOp("entries", crdt.EncodePK("e2"), crdt.OpInsert, []crdt.Cell{
		crdt.CellFromValue("payee", "bob"),
		crdt.CellFromValue("amount", int64(7)),
	})
	if op.Seq != 1 {
		t.Skipf("mushql now seeds Node's counter from the syncer's: MakeOp gave seq %d, so this collision is fixed", op.Seq)
	}
	if err := node.LocalWrite(ctx, op); err != nil {
		t.Fatalf("LocalWrite reported %v; the point of this test is that it reports nothing", err)
	}

	got := entriesOn(t, userDB)
	if _, present := got["e2"]; present {
		t.Fatal("the second write landed; mushql no longer loses it and this test should become the opposite assertion")
	}
	t.Logf("the row written through Node.LocalWrite was discarded without an error: %v", got)
}
