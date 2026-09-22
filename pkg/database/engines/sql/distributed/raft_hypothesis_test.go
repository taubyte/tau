package distributed

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/samyfodil/mushql/crdt"
	"github.com/samyfodil/mushql/engine"
	"github.com/samyfodil/mushql/puredriver"
)

// The tests in this file test ONE HYPOTHESIS: that raft is a degenerate CRDT —
// one site (the cluster), the raft log index as the timestamp, no two ops ever
// concurrent, the last-writer-wins merge never firing, and anti-entropy
// collapsing to "send me everything after index N". If that held, mushql would
// need no new interface: a raft driver would be a crdt.Transport whose GetOps
// reads the log and whose PushOp proposes to the leader.
//
// NOTHING IN MUSHQL IS MODIFIED BY ANY OF THIS. Every test here uses only
// mushql's exported surface, and the ones that fail the hypothesis fail it
// against mushql as it ships.
//
// The answer, established below: the ORDERING half of the hypothesis holds and
// the DURABILITY half does not. See raft_driver.go for what that costs.

// openLedgerStore builds a bare ledger database with a crdt.Store over it and
// no Syncer, so that the site-registry and the capture hook stay out of tests
// that are only about the apply path.
func openLedgerStore(t *testing.T, site string) (*crdt.Store, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	// ONE CONNECTION, because crdt.Wrap gives its own apply handle exactly one
	// and the materialize path writes the user table from the same *sql.DB.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(ledgerDDL); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	store, err := crdt.Open(context.Background(), db, site)
	if err != nil {
		t.Fatalf("opening a store on %s: %v", site, err)
	}
	t.Cleanup(func() { db.Close() })
	return store, db
}

// indexOp is the op a raft driver would synthesize: one site — the cluster —
// and an HLC that is the raft log index rather than a wall clock.
func indexOp(index uint64, id string, amount int64) crdt.Op {
	return crdt.Op{
		Site: "cluster",
		Seq:  index,
		HLC:  crdt.HLC(index),
		Tbl:  "entries",
		PK:   crdt.EncodePK(id),
		Kind: crdt.OpInsert,
		Cells: []crdt.Cell{
			crdt.CellFromValue("payee", "alice"),
			crdt.CellFromValue("amount", amount),
		},
	}
}

// --- 1. Does the apply path need real clocks, or only a total order? ---

// TestTheCellMergeNeedsOnlyATotalOrderNotARealClock is the first half of the
// hypothesis, and it holds.
//
// Store.mergeCell resolves a cell with TagLess((storedHLC, storedSite), (opHLC,
// opSite)) and nothing else: it never compares an HLC to the wall clock, never
// bounds the skew, and never rejects an HLC for being implausible. So HLC
// values of 1, 2, 3 — raft log indices, twelve orders of magnitude below a real
// millisecond clock — order the merge exactly as they order the log.
//
// The op that arrives SECOND but carries the LOWER index loses, which is the
// property the hypothesis needs: the log's order wins over arrival order.
func TestTheCellMergeNeedsOnlyATotalOrderNotARealClock(t *testing.T) {
	ctx := context.Background()
	store, db := openLedgerStore(t, "cluster")

	// Index 2 lands first, index 1 second: arrival order is the reverse of log
	// order, so whichever wins names the order the merge actually uses.
	if err := store.Ingest(ctx, indexOp(2, "e1", 200), false); err != nil {
		t.Fatalf("ingesting index 2: %v", err)
	}
	if err := store.Ingest(ctx, indexOp(1, "e1", 100), false); err != nil {
		t.Fatalf("ingesting index 1: %v", err)
	}

	if got := entriesOn(t, db)["e1"]; got != 200 {
		t.Fatalf("the row reads %d; index 2 must beat index 1 whatever order they arrive in", got)
	}
}

// TestARaftIndexTimestampLosesToEveryLocallyCapturedWriteForever is the first
// crack, and it is why the raft index cannot BE the HLC on a database that is
// also written through mushql's own capture path.
//
// crdt.Clock.Now stamps a captured write with 48 bits of Unix milliseconds in
// the high bits — about 1.7e12 — and TagLess compares HLCs as plain integers.
// A raft log index is a small number. So every locally captured write outranks
// every raft-ordered op the cluster will ever produce, no matter how much later
// the log entry is, and the LWW merge that the hypothesis says "never fires"
// fires on every single cell and always for the same side.
//
// A raft driver therefore may not use the raft index as the HLC unless it also
// takes the capture path away — and the capture path is the only way mushql
// turns a guest's INSERT into an op at all.
func TestARaftIndexTimestampLosesToEveryLocallyCapturedWriteForever(t *testing.T) {
	ctx := context.Background()
	syncer, userDB := wrapLedger(t, t.Name())

	if _, err := userDB.ExecContext(ctx,
		`INSERT INTO entries(id,payee,amount) VALUES('e1','local',1)`); err != nil {
		t.Fatalf("the captured local write: %v", err)
	}

	// An op the cluster committed far in the future of the log — index 1e9,
	// which no real deployment reaches — and still far in the past of a clock.
	if err := syncer.Store().Ingest(ctx, indexOp(1_000_000_000, "e1", 999), false); err != nil {
		t.Fatalf("ingesting a cluster op: %v", err)
	}

	got := entriesOn(t, userDB)["e1"]
	if got == 999 {
		t.Skip("mushql now compares captured and index timestamps in one domain; the hypothesis' clock objection is gone")
	}
	if got != 1 {
		t.Fatalf("the row reads %d, expected either 1 (the local write wins) or 999 (the cluster op wins)", got)
	}
	t.Log("a locally captured write outranks a raft op at index 1e9: the index and the capture clock are not one order")
}

// --- 2. Does anything require more than one site? ---

// TestOneSiteIsEnoughForTheStoreAndTheVersionVector is the second half of the
// hypothesis holding: nothing in Store needs a second actor.
//
// The version vector of a one-site store is one number, the oplog is one dense
// run of sequences, and OpsSince(site, N) is literally "everything after N" —
// the query a raft log answers natively.
func TestOneSiteIsEnoughForTheStoreAndTheVersionVector(t *testing.T) {
	ctx := context.Background()
	store, db := openLedgerStore(t, "cluster")

	const entries = 5
	for i := uint64(1); i <= entries; i++ {
		if err := store.Ingest(ctx, indexOp(i, fmt.Sprintf("e%d", i), int64(i)), false); err != nil {
			t.Fatalf("ingesting index %d: %v", i, err)
		}
	}

	vv := store.LocalVV()
	if len(vv) != 1 || vv["cluster"] != entries {
		t.Fatalf("the version vector is %v; one site at %d was expected", vv, entries)
	}
	if got := len(entriesOn(t, db)); got != entries {
		t.Fatalf("%d of %d rows materialized", got, entries)
	}

	// The cursor read. This is the whole of what a raft-backed GetOps has to
	// answer, and it is the shape a raft log is.
	ops, err := store.OpsSince(ctx, "cluster", 3, 0)
	if err != nil {
		t.Fatalf("reading after index 3: %v", err)
	}
	if len(ops) != 2 || ops[0].Seq != 4 || ops[1].Seq != 5 {
		t.Fatalf("after index 3 the store offers %d ops; 4 and 5 were expected", len(ops))
	}
}

// TestTwoReplicasOfOneRaftClusterCannotBeWrappedInOneProcess is a practical
// blocker rather than a conceptual one, and it lands squarely on tau.
//
// The site id is a process-global registry key in crdt.Wrap, and a raft
// deployment gives every replica of one database the SAME site — the cluster.
// tau's dream runs a whole cloud's nodes inside one process, so two replicas of
// one database are two Wrap calls with one site id, and the second is refused.
func TestTwoReplicasOfOneRaftClusterCannotBeWrappedInOneProcess(t *testing.T) {
	ctx := context.Background()
	site := t.Name() + "-cluster"

	first, firstDB := wrapLedgerAt(t, site, "replica-a")
	defer func() {
		firstDB.Close()
		first.Close()
	}()

	second, secondDB, err := crdt.Wrap(ctx, newLedgerAt(t, "replica-b"), site, []string{"entries"})
	if err == nil {
		secondDB.Close()
		second.Close()
		t.Skip("mushql now allows one site id on two databases in a process; this blocker is gone")
	}
	t.Logf("a second replica of the same cluster in one process: %v", err)
}

// newLedgerAt creates a ledger database in its own directory and returns the
// DSN, so one test can hold two replicas.
func newLedgerAt(t *testing.T, name string) string {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), name+".db")
	bare, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", dsn, err)
	}
	defer bare.Close()
	if _, err := bare.Exec(ledgerDDL); err != nil {
		t.Fatalf("creating the table in %s: %v", name, err)
	}
	return dsn
}

func wrapLedgerAt(t *testing.T, site, name string) (*crdt.Syncer, *sql.DB) {
	t.Helper()

	syncer, db, err := crdt.Wrap(context.Background(), newLedgerAt(t, name), site, []string{"entries"})
	if err != nil {
		t.Fatalf("wrapping %s as %s: %v", name, site, err)
	}
	return syncer, db
}

// --- 3. Is single-site anti-entropy coherent? ---

// TestSingleSiteAntiEntropyCarriesTheWholeLog answers the version-vector
// question with the loop actually running: two stores that share one site id,
// a transport between them, and a full log crossing on a version vector with a
// single entry.
//
// It uses bare Stores rather than Syncers because of the registry collision the
// test above pins — which is itself the reason a raft driver cannot be tested
// through crdt.Wrap at all.
func TestSingleSiteAntiEntropyCarriesTheWholeLog(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	leader, _ := openLedgerStore(t, "cluster")
	follower, followerDB := openLedgerStore(t, "cluster")

	leaderNode := crdt.NewNode(leader, nil)
	followerNode := crdt.NewNode(follower, nil)

	net := crdt.NewMemNetwork()
	leaderNode.SetTransport(net.AddNode("leader", leaderNode.OpSource()))
	followerNode.SetTransport(net.AddNode("follower", followerNode.OpSource()))

	const entries = 8
	for i := uint64(1); i <= entries; i++ {
		if err := leader.Ingest(ctx, indexOp(i, fmt.Sprintf("e%d", i), int64(i)), false); err != nil {
			t.Fatalf("the leader applying index %d: %v", i, err)
		}
	}

	go followerNode.Run(ctx, 20*time.Millisecond)

	deadline := time.Now().Add(10 * time.Second)
	for {
		if len(entriesOn(t, followerDB)) == entries {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the follower holds %d of %d rows after anti-entropy", len(entriesOn(t, followerDB)), entries)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if vv := follower.LocalVV(); vv["cluster"] != entries {
		t.Fatalf("the follower's cursor is %v; it must sit at %d", vv, entries)
	}
}

// --- 4. Can the shape express "this op is not durable yet"? ---

// refusingTransport is a peer that is always there and always says no — a
// leader a follower cannot reach, or a proposal the cluster rejected.
type refusingTransport struct{ pushes chan struct{} }

func (r *refusingTransport) Peers() []crdt.PeerID { return []crdt.PeerID{"leader"} }

func (r *refusingTransport) ExchangeVV(context.Context, crdt.PeerID, crdt.VersionVector) (crdt.VersionVector, error) {
	return nil, errRefused
}

func (r *refusingTransport) GetOps(context.Context, crdt.PeerID, string, uint64, int) ([]crdt.Op, error) {
	return nil, errRefused
}

func (r *refusingTransport) PushOp(context.Context, crdt.PeerID, crdt.Op) error {
	select {
	case r.pushes <- struct{}{}:
	default:
	}
	return errRefused
}

func (r *refusingTransport) SubscribePush(ctx context.Context) (<-chan crdt.PeerOp, error) {
	ch := make(chan crdt.PeerOp)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

var errRefused = fmt.Errorf("the cluster refused this op")

// TestTheTransportIsConsultedOnlyAfterTheWriteIsAlreadyDurable is where the
// hypothesis fails, and it fails on the point it was always going to.
//
// A CRDT writes locally first and syncs afterwards. mushql's capture path is
// built that way on purpose: the ops are written inside the user's transaction,
// the transaction commits, and only then does postCommit hand them to
// Syncer.propagate, which calls Node.Broadcast, which discards every PushOp
// error it gets. There is no return path from the transport back to the
// caller of Exec, because by the time the transport runs the Exec has returned.
//
// Raft needs the opposite: the write is not a write until the cluster has
// committed it, and a refused proposal must surface to the caller as a failed
// statement. mushql as it ships cannot express that — not because the
// transport is CRDT-shaped, but because it is consulted on the wrong side of
// the commit.
func TestTheTransportIsConsultedOnlyAfterTheWriteIsAlreadyDurable(t *testing.T) {
	ctx := context.Background()
	syncer, userDB := wrapLedger(t, t.Name())

	tr := &refusingTransport{pushes: make(chan struct{}, 1)}
	syncer.Attach(tr)

	// Every push this write produces fails. The write does not.
	if _, err := userDB.ExecContext(ctx,
		`INSERT INTO entries(id,payee,amount) VALUES('e1','alice',42)`); err != nil {
		t.Skipf("mushql now fails a write whose transport refuses it: %v — the hypothesis' durability objection is gone", err)
	}

	select {
	case <-tr.pushes:
	case <-time.After(2 * time.Second):
		t.Fatal("the transport was never consulted at all; this test cannot say anything")
	}

	if got := entriesOn(t, userDB)["e1"]; got != 42 {
		t.Fatalf("the row reads %d after a refused push; the point of this test is that it is 42", got)
	}

	// And it is not merely visible, it is in the oplog and in the version
	// vector: durable, numbered, and unreplicated.
	if seq := syncer.Store().LocalVV()[syncer.Site()]; seq != 1 {
		t.Fatalf("the version vector sits at %d; the refused op must still be numbered 1", seq)
	}
	t.Log("a write every peer refused is committed, materialized, numbered and reported as success")
}

// probeHooks is a capture consumer of our own, installed on connections
// carrying a _probe marker, so that the driver seam can be tested without
// touching crdt.
type probeHooks struct {
	refuse bool
	saw    int
}

// TestTheDriverCanRefuseACommitThoughTheCrdtLayerOffersNoWayTo is the other
// half of the previous result, and it is the good news.
//
// puredriver.CaptureHooks.PreCommit runs INSIDE the transaction about to
// commit and its error aborts that transaction. So the seam a raft driver needs
// — propose, wait for the cluster, and fail the statement if the cluster says
// no — exists in mushql's driver already.
//
// What does not exist is a way to reach it: crdt.Wrap installs its own
// CaptureHooks on every connection carrying its marker, Syncer exposes no way
// to interpose on them, and a second connection hook that installed its own
// would replace crdt's capture rather than wrap it (SetCaptureHooks takes one
// struct, not a chain). The mechanism is there and the crdt layer is built
// across it.
func TestTheDriverCanRefuseACommitThoughTheCrdtLayerOffersNoWayTo(t *testing.T) {
	ctx := context.Background()

	probe := &probeHooks{refuse: true}
	puredriver.RegisterConnectionHook(func(conn *puredriver.Conn, dsn string) error {
		if !hasProbeMarker(dsn, t.Name()) {
			return nil
		}
		conn.SetCaptureHooks(&puredriver.CaptureHooks{
			PreCommit: func(context.Context, *puredriver.Conn, []engine.RowChange) error {
				probe.saw++
				if probe.refuse {
					return errRefused
				}
				return nil
			},
		})
		return nil
	})

	dsn := newLedgerAt(t, "probe")
	db, err := sql.Open("sqlite", dsn+"?_probe="+t.Name())
	if err != nil {
		t.Fatalf("opening the probed handle: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO entries(id,payee,amount) VALUES('e1','alice',42)`); err == nil {
		t.Fatal("a write whose PreCommit returned an error succeeded; the driver seam cannot refuse a commit")
	}
	if probe.saw == 0 {
		t.Fatal("PreCommit never ran; this test cannot say anything")
	}
	if got := entriesOn(t, db); len(got) != 0 {
		t.Fatalf("the refused row is present: %v; a refused PreCommit must roll the transaction back", got)
	}

	// The same handle commits once the hook stops refusing, so what was proved
	// is a refusal and not a broken connection.
	probe.refuse = false
	if _, err := db.ExecContext(ctx,
		`INSERT INTO entries(id,payee,amount) VALUES('e2','bob',7)`); err != nil {
		t.Fatalf("the write the hook allowed: %v", err)
	}
	if got := entriesOn(t, db)["e2"]; got != 7 {
		t.Fatalf("the allowed row reads %d, wrote 7", got)
	}
}

func hasProbeMarker(dsn, name string) bool {
	return len(dsn) > len(name) && dsn[len(dsn)-len(name):] == name
}

// TestAVersionVectorAheadOfTheOplogFallsBackOnRestart is the third reason the
// durability half fails, and the quietest.
//
// capture.postCommit advances the version vector with Store.NoteLocal, which
// sets the site's entry straight to the highest sequence that committed — past
// any number a retried commit took and abandoned. Store.loadVV, run when the
// database is reopened, instead walks the oplog CONTIGUOUSLY and stops at the
// first missing number.
//
// So the cursor a node offers its peers is one value while the process lives
// and a smaller one after it restarts, and every op between them stops being
// advertised. Under raft — where the single site is the whole cluster — that is
// the entire database's replication, stopped by one refused proposal.
func TestAVersionVectorAheadOfTheOplogFallsBackOnRestart(t *testing.T) {
	ctx := context.Background()

	dsn := newLedgerAt(t, "cursor")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	db.SetMaxOpenConns(1)

	store, err := crdt.Open(ctx, db, "cluster")
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	for i := uint64(1); i <= 3; i++ {
		if err := store.Ingest(ctx, indexOp(i, fmt.Sprintf("e%d", i), int64(i)), false); err != nil {
			t.Fatalf("ingesting index %d: %v", i, err)
		}
	}

	// What postCommit does after a retry burned 4 through 8: name the highest
	// number that committed, without checking that the ones below it exist.
	store.NoteLocal("cluster", 9)
	if got := store.LocalVV()["cluster"]; got != 9 {
		t.Fatalf("the in-memory cursor is %d; NoteLocal was asked for 9", got)
	}
	db.Close()

	reopened, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("reopening the database: %v", err)
	}
	defer reopened.Close()
	reopened.SetMaxOpenConns(1)

	restarted, err := crdt.Open(ctx, reopened, "cluster")
	if err != nil {
		t.Fatalf("reopening the store: %v", err)
	}
	if got := restarted.LocalVV()["cluster"]; got == 9 {
		t.Skip("mushql now persists the advanced cursor; this failure mode is gone")
	} else if got != 3 {
		t.Fatalf("the reopened cursor is %d; 3 (the last contiguous op) was expected", got)
	}
	t.Log("the cursor was 9 in memory and 3 after a restart: every op in between stops being offered to peers")
}
