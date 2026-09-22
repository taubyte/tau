package distributed

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/samyfodil/mushql/crdt"
	_ "github.com/samyfodil/mushql/puredriver"
)

// The tests in this file are not about tau. They pin down what mushql's CRDT
// layer does when the schema is not known before the database is wrapped,
// which is the ONE thing a tau `sql` database cannot avoid: the vocabulary
// declares a sql database with a size and a consensus and no tables at all, so
// every table it will ever hold is created by a guest's `CREATE TABLE` after
// the engine already has the database open.
//
// They exist so that the answer is a test result rather than a reading of the
// source, and so a later mushql changes its mind loudly.

const moreDDL = `CREATE TABLE more (id TEXT PRIMARY KEY, note TEXT)`

// newLedger creates a database file with the ledger schema and returns its DSN.
func newLedger(t *testing.T) string {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), "ledger.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", dsn, err)
	}
	defer db.Close()
	if _, err := db.Exec(ledgerDDL); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	return dsn
}

func TestWrappingATableThatDoesNotExistYetIsRefused(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "empty.db")

	// The file has to exist as a database before anything can be read out of
	// it; an empty one with no tables is the state a tau sql database is in
	// the moment it is created and before any guest has run DDL.
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", dsn, err)
	}
	if _, err := db.Exec(`CREATE TABLE placeholder (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("creating a table: %v", err)
	}
	db.Close()

	syncer, userDB, err := crdt.Wrap(context.Background(), dsn, t.Name(), []string{"entries"})
	if err == nil {
		userDB.Close()
		syncer.Close()
		t.Fatal("wrapping a table that does not exist succeeded; it must not")
	}
	t.Logf("wrapping a table that does not exist yet: %v", err)
}

// TestWrappingNoTablesAtAllSucceeds establishes the shape an engine would have
// to open with, since it has no table names to give.
func TestWrappingNoTablesAtAllSucceeds(t *testing.T) {
	dsn := newLedger(t)

	syncer, userDB, err := crdt.Wrap(context.Background(), dsn, t.Name(), nil)
	if err != nil {
		t.Fatalf("wrapping no tables: %v", err)
	}
	defer func() {
		userDB.Close()
		syncer.Close()
	}()

	if _, err := userDB.Exec(`INSERT INTO entries(id,payee,amount) VALUES('e1','alice',42)`); err != nil {
		t.Fatalf("writing to an unsynced table: %v", err)
	}

	// Nothing was captured, which is the point: a table outside the wrapped
	// set is a table whose writes never leave this node, silently.
	vv := syncer.Store().LocalVV()
	if seq := vv[syncer.Site()]; seq != 0 {
		t.Fatalf("an unsynced write produced %d ops; it must produce none", seq)
	}
}

// TestAddingATableToTheSyncedSetAfterTheFirstWrapIsRefused is the blocker.
//
// The only supported way to start syncing a table mushql was not told about at
// the first Wrap is to Wrap again with the larger set — and that is exactly
// what the schema fingerprint, stored on the first Wrap and never updated,
// refuses.
func TestAddingATableToTheSyncedSetAfterTheFirstWrapIsRefused(t *testing.T) {
	dsn := newLedger(t)
	ctx := context.Background()

	syncer, userDB, err := crdt.Wrap(ctx, dsn, t.Name(), []string{"entries"})
	if err != nil {
		t.Fatalf("the first wrap: %v", err)
	}

	// A guest creates a second table through the handle it was given.
	if _, err := userDB.ExecContext(ctx, moreDDL); err != nil {
		t.Fatalf("creating a second table: %v", err)
	}
	userDB.Close()
	syncer.Close()

	syncer, userDB, err = crdt.Wrap(ctx, dsn, t.Name(), []string{"entries", "more"})
	if err == nil {
		userDB.Close()
		syncer.Close()
		t.Fatal("rewrapping with the new table succeeded; if mushql now allows this, the engine can follow a guest's DDL")
	}
	if !errors.Is(err, crdt.ErrSchemaChanged) {
		t.Fatalf("rewrapping with the new table failed with %v, wanted crdt.ErrSchemaChanged", err)
	}
	t.Logf("rewrapping with a table the guest just created: %v", err)
}

// TestRewrappingTheSameTableSetSucceeds separates the two halves of the
// previous result: reopening is fine, and DDL on a table OUTSIDE the synced
// set does not poison the fingerprint. It is only growing the set that is
// refused — so an engine can survive a restart, and can survive a guest
// creating tables, but can never begin replicating one of them.
func TestRewrappingTheSameTableSetSucceeds(t *testing.T) {
	dsn := newLedger(t)
	ctx := context.Background()

	syncer, userDB, err := crdt.Wrap(ctx, dsn, t.Name(), []string{"entries"})
	if err != nil {
		t.Fatalf("the first wrap: %v", err)
	}
	if _, err := userDB.ExecContext(ctx, moreDDL); err != nil {
		t.Fatalf("creating a second table: %v", err)
	}
	userDB.Close()
	syncer.Close()

	syncer, userDB, err = crdt.Wrap(ctx, dsn, t.Name(), []string{"entries"})
	if err != nil {
		t.Fatalf("rewrapping the same table set: %v", err)
	}
	userDB.Close()
	syncer.Close()
}
