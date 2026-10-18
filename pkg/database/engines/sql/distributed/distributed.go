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

// Name is the engine name the vocabulary uses for tau's own.
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

// UnderRoot is the Files provider a node composes: one directory per database
// beneath the node's own root, named by database.Spec.Prefix.
//
// THE PREFIX BECOMES ONE PATH SEGMENT, escaped the way node/volume escapes a
// volume handle: a prefix carries slashes, and a directory per segment would
// put one project's databases inside another project's name.
//
// A NODE WITH NO ROOT REFUSES, and does not fall back to a temp directory: a
// SQL database served out of one loses every row when the process ends, and it
// loses them silently.
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

// escapePrefix turns a database.Spec.Prefix into one path segment.
func escapePrefix(prefix string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	return strings.ReplaceAll(strings.ReplaceAll(prefix, "%", "%25"), "/", "%2F")
}

// InDir is the Files provider for a test and for a node that keeps every
// database under one directory it already chose. It is spelled out rather than
// left as a default, the way database.OneStore is, so that choosing it is
// visible.
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

// Engine backs the sql type with a musql database per declared database.
type Engine struct {
	files Files
	sync  func(topic string) replication.Option
}

var _ database.Engine = (*Engine)(nil)

// Option configures an Engine.
type Option func(*Engine)

// WithSync is how a database's copies reach each other: handed the database's
// own topic, it answers the replication transport for it. A node passes
// replication/libp2p's SyncWith over its own host and pubsub router.
//
// WITHOUT ONE A DATABASE IS STILL CAPTURED: every write goes into its op log,
// and a peer that later syncs picks it up. It is not served from nowhere.
func WithSync(sync func(topic string) replication.Option) Option {
	return func(e *Engine) { e.sync = sync }
}

// New builds the engine over a directory provider.
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

// Types is sql and only sql.
func (e *Engine) Types() []database.Type { return []database.Type{database.SQL} }

// Open answers a connection onto one database of one deployment.
//
// AN UNADDRESSABLE SPEC IS A CONNECTION THAT FAILS, NOT AN OPEN THAT DOES, for
// the reason pkg/database/engines/kv gives: pkg/database's registration probe
// opens one connection per type with no project and no environment to prove the
// SURFACE, and an engine that refused it could not be registered at all. Here
// it matters twice over — an engine that asked its provider for a directory on
// the probe would leave a `probe` database on the node's disk.
//
// IT DOES NOT TOUCH THE FILE. database/sql opens lazily and musql creates the
// database on the first statement, so registration proves the surface without
// writing anything.
//
// ONLY ErrUnaddressable IS DEFERRED, and not every addressing failure the way
// engines/kv defers them. The other one is ErrSeparator — a database named
// `../../production/ledger` — and the probe never produces it. Here that name
// is a path out of the project's own directory rather than a key prefix, so it
// is refused where it is written rather than at whatever operation the caller
// tries next.
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

	// THE MODE IS THE DECLARATION'S. crdt is every node writing and converging;
	// raft is refused by name, see ErrRaft. Anything else is a word this engine
	// does not serve.
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
	// THE DECLARED CEILING, held by musql in its own format's bytes: a commit
	// that would leave the file over it fails with "database or disk is full"
	// and writes nothing. In crdt mode it bounds what this node writes, not
	// what peers' merges may add — a refused merge would never converge.
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

// conn is one database, wearing the operation surface the vocabulary declares
// for `sql`: query and exec, and nothing else.
//
// It mirrors pkg/database/engines/sql's conn rather than sharing it, because
// that one is reached through a DSN function that cannot refuse the
// registration probe without failing registration, and because this one is
// where a musql Syncer and its replication node will have to be held.
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

// Exec answers what the statement changed. A driver that cannot say is not an
// error, and each half is asked independently — the same rule
// pkg/database/engines/sql keeps, and for the same reason.
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
