package distributed_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/sql/distributed"
)

func ledger(t *testing.T) database.Spec {
	t.Helper()
	return database.Spec{
		Type:        database.SQL,
		Engine:      distributed.Name,
		Project:     "acme",
		Environment: "prod",
		Name:        "ledger",
	}
}

func TestAnEngineWithNoDirectoryProviderIsRefused(t *testing.T) {
	if _, err := distributed.New(nil); !errors.Is(err, distributed.ErrNoFiles) {
		t.Fatalf("building an engine with no provider gave %v, wanted ErrNoFiles", err)
	}
}

// TestRegisteringProvesTheSurfaceWithoutTouchingTheDisk is the registration
// probe's whole point on this engine: pkg/database opens a connection with no
// project and no environment, and an engine that asked its provider for a
// directory then would leave a `probe` database on the node's disk.
func TestRegisteringProvesTheSurfaceWithoutTouchingTheDisk(t *testing.T) {
	dir := t.TempDir()

	asked := 0
	files := func(ctx context.Context, spec database.Spec) (string, error) {
		asked++
		return distributed.InDir(dir)(ctx, spec)
	}

	engine, err := distributed.New(files)
	if err != nil {
		t.Fatalf("building the engine: %v", err)
	}

	registry := database.NewRegistry()
	if err := registry.Register(context.Background(), engine); err != nil {
		t.Fatalf("registering the engine: %v", err)
	}
	if asked != 0 {
		t.Fatalf("the registration probe asked the provider for a directory %d times; it must ask none", asked)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 0 {
		t.Fatalf("registration left %v on disk; it must leave nothing", entries)
	}

	if names := registry.EnginesFor(database.SQL); len(names) != 1 || names[0] != distributed.Name {
		t.Fatalf("sql is backed by %v, wanted just %q", names, distributed.Name)
	}
}

func TestADatabaseKeepsItsRowsAcrossOpens(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	engine, err := distributed.New(distributed.InDir(dir))
	if err != nil {
		t.Fatalf("building the engine: %v", err)
	}

	open := func() database.SQLConn {
		t.Helper()
		c, err := engine.Open(ctx, ledger(t))
		if err != nil {
			t.Fatalf("opening the database: %v", err)
		}
		sqlConn, ok := c.(database.SQLConn)
		if !ok {
			t.Fatalf("the engine answered a %T, which is not a database.SQLConn", c)
		}
		return sqlConn
	}

	first := open()
	if _, err := first.Exec(ctx, `CREATE TABLE entries (id TEXT PRIMARY KEY, amount INTEGER)`); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	outcome, err := first.Exec(ctx, `INSERT INTO entries(id,amount) VALUES(?,?)`, "e1", 42)
	if err != nil {
		t.Fatalf("inserting: %v", err)
	}
	if outcome.Affected != 1 {
		t.Fatalf("the insert reported %d rows affected, wanted 1", outcome.Affected)
	}
	first.Close()

	second := open()
	defer second.Close()

	result, err := second.Query(ctx, `SELECT id, amount FROM entries`)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	defer result.Close()

	columns, err := result.Columns()
	if err != nil {
		t.Fatalf("asking for the columns: %v", err)
	}
	if len(columns) != 2 || columns[0] != "id" || columns[1] != "amount" {
		t.Fatalf("the result set names %v, wanted id and amount", columns)
	}

	var seen int
	for result.Next() {
		var id string
		var amount int64
		if err := result.Scan(&id, &amount); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		if id != "e1" || amount != 42 {
			t.Fatalf("read back %q/%d, wrote e1/42", id, amount)
		}
		seen++
	}
	if err := result.Err(); err != nil {
		t.Fatalf("iterating: %v", err)
	}
	if seen != 1 {
		t.Fatalf("the second open saw %d rows, wanted the 1 the first wrote", seen)
	}
}

// TestTwoDatabasesOfOneProjectAreSeparateFiles is the isolation Spec.Prefix
// exists for, at the level this engine works at: directories, not key prefixes.
func TestTwoDatabasesOfOneProjectAreSeparateFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	engine, err := distributed.New(distributed.InDir(dir))
	if err != nil {
		t.Fatalf("building the engine: %v", err)
	}

	ledgerSpec := ledger(t)
	auditSpec := ledger(t)
	auditSpec.Name = "audit"

	for _, spec := range []database.Spec{ledgerSpec, auditSpec} {
		c, err := engine.Open(ctx, spec)
		if err != nil {
			t.Fatalf("opening %s: %v", spec.Name, err)
		}
		if _, err := c.(database.SQLConn).Exec(ctx, `CREATE TABLE only_here (id TEXT PRIMARY KEY)`); err != nil {
			t.Fatalf("creating a table in %s: %v", spec.Name, err)
		}
		c.Close()
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	if len(entries) != 2 {
		t.Fatalf("two databases produced %d directories: %v", len(entries), names(entries))
	}
	for _, entry := range entries {
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), "database.db")); err != nil {
			t.Fatalf("%s holds no database file: %v", entry.Name(), err)
		}
	}
}

// TestADatabaseNamedWithASeparatorIsRefused is the escape hatch out of a
// project's own directory, and it is closed by Spec.Prefix rather than here.
func TestADatabaseNamedWithASeparatorIsRefused(t *testing.T) {
	ctx := context.Background()

	engine, err := distributed.New(distributed.InDir(t.TempDir()))
	if err != nil {
		t.Fatalf("building the engine: %v", err)
	}

	spec := ledger(t)
	spec.Name = "../../production/ledger"

	if _, err := engine.Open(ctx, spec); !errors.Is(err, database.ErrSeparator) {
		t.Fatalf("opening a database whose name escapes its project gave %v, wanted ErrSeparator", err)
	}
}

func TestANodeWithNoRootHasNowhereToKeepASQLDatabase(t *testing.T) {
	if _, err := distributed.UnderRoot("")(context.Background(), ledger(t)); !errors.Is(err, distributed.ErrNoRoot) {
		t.Fatalf("a rootless node gave %v, wanted ErrNoRoot", err)
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	return out
}

// TestConcurrentWritersThroughOneConnectionAllLand is not a mushql unit test
// wearing tau's clothes: it is the shape every tau sql database is used in.
// One database.SQLConn is shared by every guest invocation placed on the node,
// so its *sql.DB pool hands out several connections at once and several
// statements write the same file at the same time.
//
// mushql's driver doc says a second writer's commit fails with engine.ErrBusy
// and that autocommit statements retry it — this is what asks whether the
// retry is enough, since `exec` in this vocabulary is one statement with no
// transaction around it.
func TestConcurrentWritersThroughOneConnectionAllLand(t *testing.T) {
	ctx := context.Background()

	engine, err := distributed.New(distributed.InDir(t.TempDir()))
	if err != nil {
		t.Fatalf("building the engine: %v", err)
	}
	c, err := engine.Open(ctx, ledger(t))
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	defer c.Close()
	conn := c.(database.SQLConn)

	if _, err := conn.Exec(ctx, `CREATE TABLE entries (id TEXT PRIMARY KEY, amount INTEGER)`); err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	const writers = 16
	errs := make(chan error, writers)
	var start sync.WaitGroup
	start.Add(1)
	for i := 0; i < writers; i++ {
		go func(i int) {
			start.Wait()
			_, err := conn.Exec(ctx, `INSERT INTO entries(id,amount) VALUES(?,?)`, fmt.Sprintf("e%02d", i), i)
			errs <- err
		}(i)
	}
	start.Done()

	var failed int
	for i := 0; i < writers; i++ {
		if err := <-errs; err != nil {
			failed++
			t.Logf("a concurrent writer failed: %v", err)
		}
	}
	if failed != 0 {
		t.Fatalf("%d of %d concurrent single-statement writes failed; tau's `exec` has no transaction to retry in", failed, writers)
	}

	rows, err := conn.Query(ctx, `SELECT count(*) FROM entries`)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("counting returned no row")
	}
	var count int64
	if err := rows.Scan(&count); err != nil {
		t.Fatalf("scanning the count: %v", err)
	}
	if count != writers {
		t.Fatalf("%d of %d rows landed", count, writers)
	}
}
