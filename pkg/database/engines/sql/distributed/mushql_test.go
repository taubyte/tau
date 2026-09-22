package distributed

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/samyfodil/mushql/crdt"

	// The driver registers itself as "sqlite" from its own init, exactly as
	// pkg/database/engines/sql's doc describes for every other driver. It is
	// imported for that side effect here and named nowhere else.
	_ "github.com/samyfodil/mushql/puredriver"
)

// ledgerDDL is the shape a synced table must have: mushql's CRDT captures rows
// by a single TEXT or BLOB primary key on an ordinary rowid table, so an
// INTEGER PRIMARY KEY or a composite one is refused by Wrap.
const ledgerDDL = `CREATE TABLE entries (
	id     TEXT PRIMARY KEY,
	payee  TEXT,
	amount INTEGER
)`

func TestAMushqlDatabaseOpensAndAnswersAQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer db.Close()

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, ledgerDDL); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO entries(id,payee,amount) VALUES(?,?,?)`, "e1", "alice", 42); err != nil {
		t.Fatalf("inserting: %v", err)
	}

	var payee string
	var amount int64
	if err := db.QueryRowContext(ctx, `SELECT payee, amount FROM entries WHERE id = ?`, "e1").Scan(&payee, &amount); err != nil {
		t.Fatalf("selecting: %v", err)
	}
	if payee != "alice" || amount != 42 {
		t.Fatalf("read back %q/%d, wrote alice/42", payee, amount)
	}
}

// wrapLedger creates a database with the ledger schema and hands back a
// CRDT-wrapped handle to it.
//
// THE DDL RUNS ON A SEPARATE HANDLE THAT IS CLOSED FIRST, because crdt.Wrap
// reads the schema of every table it is asked to sync and fails with
// `crdt: table %q not found` if it is not already there. A tau SQL database has
// no schema until a guest issues its first CREATE TABLE, which is the gap
// between this test and a real engine.
func wrapLedger(t *testing.T, site string) (*crdt.Syncer, *sql.DB) {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), site+".db")

	bare, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", dsn, err)
	}
	if _, err := bare.Exec(ledgerDDL); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	bare.Close()

	syncer, db, err := crdt.Wrap(context.Background(), dsn, site, []string{"entries"})
	if err != nil {
		t.Fatalf("wrapping %s: %v", site, err)
	}
	t.Cleanup(func() {
		db.Close()
		syncer.Close()
	})
	return syncer, db
}

func entriesOn(t *testing.T, db *sql.DB) map[string]int64 {
	t.Helper()

	rows, err := db.Query(`SELECT id, amount FROM entries ORDER BY id`)
	if err != nil {
		t.Fatalf("listing entries: %v", err)
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var id string
		var amount int64
		if err := rows.Scan(&id, &amount); err != nil {
			t.Fatalf("scanning an entry: %v", err)
		}
		out[id] = amount
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing entries: %v", err)
	}
	return out
}

// TestAWriteOnOneNodeArrivesOnTheOther is the step that proves replication
// works from OUTSIDE mushql: two wrapped databases, a transport between them,
// and a row written through one handle read back through the other's.
func TestAWriteOnOneNodeArrivesOnTheOther(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The site id is the CRDT actor id AND a process-global registry key in
	// mushql, so two tests using the same one in one binary collide. The test
	// name is what keeps them apart.
	left, leftDB := wrapLedger(t, t.Name()+"-left")
	right, rightDB := wrapLedger(t, t.Name()+"-right")

	net := crdt.NewMemNetwork()
	leftTr := net.AddNode(crdt.PeerID(left.Site()), left.OpSource())
	rightTr := net.AddNode(crdt.PeerID(right.Site()), right.OpSource())

	leftNode := left.Attach(leftTr)
	rightNode := right.Attach(rightTr)

	go leftNode.Run(ctx, 20*time.Millisecond)
	go rightNode.Run(ctx, 20*time.Millisecond)

	if _, err := leftDB.ExecContext(ctx, `INSERT INTO entries(id,payee,amount) VALUES(?,?,?)`, "e1", "alice", 42); err != nil {
		t.Fatalf("writing on the left: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		got := entriesOn(t, rightDB)
		if got["e1"] == 42 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the left node's write never arrived on the right: %v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// And the other direction, on the same pair, so this is not one-way luck.
	if _, err := rightDB.ExecContext(ctx, `INSERT INTO entries(id,payee,amount) VALUES(?,?,?)`, "e2", "bob", 7); err != nil {
		t.Fatalf("writing on the right: %v", err)
	}

	deadline = time.Now().Add(10 * time.Second)
	for {
		got := entriesOn(t, leftDB)
		if got["e2"] == 7 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the right node's write never arrived on the left: %v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestConcurrentWritesThroughACapturedHandleAreAllCaptured asks the question a
// tau node's shape forces: one database.SQLConn is shared by every guest
// invocation on the node, so a CRDT-wrapped handle sees several single-
// statement writes at once. mushql's capture hook runs INSIDE each
// transaction, writing the oplog and the clock on that same connection, and
// the driver documents that a second writer's commit fails with ErrBusy.
//
// Every write must land AND every write must be captured — a write that
// commits without an op is a row that never replicates, which is worse than a
// write that fails.
func TestConcurrentWritesThroughACapturedHandleAreAllCaptured(t *testing.T) {
	ctx := context.Background()

	syncer, userDB := wrapLedger(t, t.Name())

	const writers = 16
	errs := make(chan error, writers)
	var start sync.WaitGroup
	start.Add(1)
	for i := 0; i < writers; i++ {
		go func(i int) {
			start.Wait()
			_, err := userDB.ExecContext(ctx,
				`INSERT INTO entries(id,payee,amount) VALUES(?,?,?)`, fmt.Sprintf("e%02d", i), "alice", i)
			errs <- err
		}(i)
	}
	start.Done()

	var failed int
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			failed++
			t.Logf("a concurrent captured writer failed: %v", err)
		}
	}
	if failed != 0 {
		t.Fatalf("%d of %d concurrent writes through a captured handle failed", failed, writers)
	}

	if got := len(entriesOn(t, userDB)); got != writers {
		t.Fatalf("%d of %d rows landed", got, writers)
	}

	ops, err := syncer.Store().OpsSince(ctx, syncer.Site(), 0, 0)
	if err != nil {
		t.Fatalf("reading the oplog: %v", err)
	}
	if len(ops) != writers {
		t.Fatalf("%d writes produced %d ops; every write must be captured exactly once", writers, len(ops))
	}

	// THE SEQ NUMBERS ARE NOT CONTIGUOUS AND THE VERSION VECTOR IS AHEAD OF
	// THEM. A contended commit is retried by the driver, capture runs again on
	// each attempt and takes another number from the syncer's counter, and the
	// numbers the abandoned attempts took are never written. postCommit then
	// advances the version vector straight to the highest number that DID
	// commit. The ops are all here and all correct; what is broken is the
	// claim the version vector makes about them, and what that costs is
	// gaps_test.go.
	seqs := make([]uint64, 0, len(ops))
	for _, op := range ops {
		seqs = append(seqs, op.Seq)
	}
	t.Logf("%d rows, %d ops, version vector at %d, seqs %v",
		writers, len(ops), syncer.Store().LocalVV()[syncer.Site()], seqs)
}
