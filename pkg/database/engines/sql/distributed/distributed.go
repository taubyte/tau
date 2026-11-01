package distributed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samyfodil/musql/replication"

	"github.com/taubyte/tau/pkg/database"
)

const Name = "distributed"

// fileName is the database inside one database's own directory.
//
// A DIRECTORY PER DATABASE AND NOT A BARE FILE, because a SQLite database is
// not one file while it is open — a journal or a WAL and its shared-memory
// index sit beside it — and a directory is also the only unit a later Destroy
// can remove without knowing which of those exist.
const fileName = "database.db"

var (
	// ErrRaft is a `sql` database that declared raft. musql commits through a
	// caller's consensus log (replication.Leader with a Quorum), which needs a
	// log that applies an opaque entry on every node in order -- and pkg/raft's
	// state machine is key-value only. Refused by name until it has that, rather
	// than served as something weaker than raft.
	ErrRaft = errors.New("distributed: a raft sql database needs a consensus log this node cannot give it yet")

	ErrNoFiles   = errors.New("distributed: no directory provider to open a database in")
	ErrWrongType = errors.New("distributed: this engine backs sql and nothing else")
	ErrNoRoot    = errors.New("distributed: a node with no root has nowhere to keep a sql database")
)

// Files hands the engine the directory ONE database's files live in, ready to
// be written to.
//
// A PROVIDER AND NOT A ROOT, for the reason database.Stores is a provider and
// not a store: where a database's bytes go is the composer's decision, and a
// test wants a temp dir without a node around it. It is Files and not Stores
// because a SQL database is a file rather than rows behind a key — see the
// package doc.
type Files func(ctx context.Context, spec database.Spec) (dir string, err error)

// UnderRoot is the Files provider a node composes: one directory per database under
// the node's root, named by Spec.Prefix escaped into one segment (as node/volume
// escapes a handle). Without a root it refuses rather than using a temp directory,
// which would lose every row silently.
func UnderRoot(root string) Files {
	return func(_ context.Context, spec database.Spec) (string, error) {
		if root == "" {
			return "", ErrNoRoot
		}
		prefix, err := spec.Prefix()
		if err != nil {
			return "", err
		}
		dir := filepath.Join(root, "databases", escapePrefix(prefix))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", fmt.Errorf("distributed: making a database directory: %w", err)
		}
		return dir, nil
	}
}

func escapePrefix(prefix string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	return strings.ReplaceAll(strings.ReplaceAll(prefix, "%", "%25"), "/", "%2F")
}

// InDir is the Files provider for a test, or a node keeping every database under one
// chosen directory; explicit so the choice is visible.
func InDir(dir string) Files {
	return func(_ context.Context, spec database.Spec) (string, error) {
		prefix, err := spec.Prefix()
		if err != nil {
			return "", err
		}
		sub := filepath.Join(dir, escapePrefix(prefix))
		if err := os.MkdirAll(sub, 0o750); err != nil {
			return "", fmt.Errorf("distributed: making a database directory: %w", err)
		}
		return sub, nil
	}
}

type Engine struct {
	files Files
	sync  func(topic string) replication.Option
}

var _ database.Engine = (*Engine)(nil)

type Option func(*Engine)

// WithSync is how a database's copies reach each other: given the database's topic,
// it answers the replication transport (replication/libp2p's SyncWith on a node).
// Without it writes still land in the op log for a later sync.
func WithSync(sync func(topic string) replication.Option) Option {
	return func(e *Engine) { e.sync = sync }
}

func New(files Files, opts ...Option) (*Engine, error) {
	if files == nil {
		return nil, ErrNoFiles
	}
	e := &Engine{files: files}
	for _, apply := range opts {
		apply(e)
	}
	return e, nil
}

// topicOf is the replication topic of one database: its prefix, which already
// names the project, the environment and the database, under tau's own root so
// it shares a pubsub router with nothing else's.
func topicOf(prefix string) string { return "tau/sql/" + strings.TrimSuffix(prefix, "/") }

func (e *Engine) Name() string { return Name }

func (e *Engine) Types() []database.Type { return []database.Type{database.SQL} }

// Open answers a connection onto one database. An unaddressable spec yields a failing
// connection, since the registry's probe has no address and must leave no `probe`
// database on disk. It does not touch the file: database/sql opens lazily and musql
// creates the database on the first statement. ErrSeparator is refused here, since
// the name becomes a path.
func (e *Engine) Open(ctx context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Type != database.SQL {
		return nil, fmt.Errorf("%w: was asked for %q", ErrWrongType, spec.Type)
	}

	prefix, err := spec.Prefix()
	switch {
	case errors.Is(err, database.ErrUnaddressable):
		return &conn{err: err}, nil
	case err != nil:
		return nil, err
	}

	// crdt converges; raft is refused (ErrRaft).
	var mode replication.Option
	switch spec.Consensus {
	case database.CRDT, "":
		mode = replication.CRDT()
	case database.Raft:
		return nil, fmt.Errorf("%w: %s", ErrRaft, prefix)
	default:
		return nil, fmt.Errorf("distributed: consensus %q is not one a sql database is served with", spec.Consensus)
	}

	dir, err := e.files(ctx, spec)
	if err != nil {
		return nil, err
	}

	opts := []replication.Option{mode}
	// The declared ceiling, enforced by musql: a commit leaving the file over it fails
	// and writes nothing. In crdt mode it bounds this node's writes, not peers' merges.
	if spec.Size > 0 {
		opts = append(opts, replication.WithMaxSize(spec.Size))
	}
	if e.sync != nil {
		opts = append(opts, e.sync(topicOf(prefix)))
	}
	db, err := replication.Open(ctx, filepath.Join(dir, fileName), opts...)
	if err != nil {
		return nil, fmt.Errorf("distributed: opening a database in %s: %w", dir, err)
	}
	return &conn{db: db}, nil
}

// conn is one database with the `sql` surface (query, exec). Not shared with
// engines/sql's conn, whose DSN path cannot defer the probe, and which will not hold
// a musql Syncer.
type conn struct {
	db *sql.DB

	// err is why this connection addresses nothing, when it does. Every
	// operation answers it — see Open.
	err error
}

var _ database.SQLConn = (*conn)(nil)

func (c *conn) Query(ctx context.Context, statement string, args ...any) (database.Rows, error) {
	if c.err != nil {
		return nil, c.err
	}
	r, err := c.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	return &rows{Rows: r}, nil
}

// Exec answers what the statement changed; a driver that cannot say is not an error.
func (c *conn) Exec(ctx context.Context, statement string, args ...any) (database.Outcome, error) {
	if c.err != nil {
		return database.Outcome{}, c.err
	}

	result, err := c.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return database.Outcome{}, err
	}

	var out database.Outcome
	if affected, err := result.RowsAffected(); err == nil {
		out.Affected = affected
	}
	if id, err := result.LastInsertId(); err == nil {
		out.LastInsertID = id
	}
	return out, nil
}

// Close releases the pool. A connection that addresses nothing has none.
func (c *conn) Close() error {
	if c.db == nil {
		return nil
	}
	return c.db.Close()
}

// rows adapts database/sql's result set to the seam's.
type rows struct{ *sql.Rows }

func (r *rows) Columns() ([]string, error) { return r.Rows.Columns() }
