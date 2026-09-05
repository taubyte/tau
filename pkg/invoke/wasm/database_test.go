package wasm_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// EVERY TEST HERE DRIVES THE REAL COMPONENT. The guest renders each value with
// its CASE visible — "integer:-42" rather than "-42" — which is what proves a
// number crossed as a number rather than as its digits.

// server is a database, as this tier sees one.
type server struct {
	mu sync.Mutex

	rows     wasm.Rows
	affected int64
	err      error

	statements []string
	bound      [][]any
}

func (s *server) Query(_ context.Context, _ invoke.Grant, statement string, args []any) (wasm.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, statement)
	s.bound = append(s.bound, args)
	if s.err != nil {
		return wasm.Rows{}, s.err
	}
	return s.rows, nil
}

func (s *server) Exec(_ context.Context, _ invoke.Grant, statement string, args []any) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statements = append(s.statements, statement)
	s.bound = append(s.bound, args)
	if s.err != nil {
		return 0, s.err
	}
	return s.affected, nil
}

func (s *server) seen() ([]string, [][]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.statements...), append([][]any(nil), s.bound...)
}

// dbGrant is a SQL database grant. The `type` setting is what selects the
// interface — four data models live under one kind — so a grant without it
// reaches neither.
func dbGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{
		Kind: "database", Label: label, Handle: handle, Ops: ops,
		Settings: map[string]string{"type": "sql"},
	}}
}

// ── the round trip ───────────────────────────────────────────────────────────

// TestAGuestQueriesAndReadsTheRows, with every value case crossing back as
// itself.
func TestAComponentQueries(t *testing.T) {
	db := &server{rows: wasm.Rows{
		Columns: []string{"id", "name", "score", "live", "blob", "missing"},
		Values: []any{
			int64(7), "alice", 1.5, true, []byte{1, 2, 3}, nil,
			int64(8), "bob", 2.5, false, []byte{4}, nil,
		},
	}}

	instance := start(t, tier(t, cacher(t), wasm.WithDatabases(db)), invoke.Spec{
		Name: "fn", Reaches: dbGrant("ledger", "h", "query", "exec"),
	})

	res := call(t, instance, "/query/ledger/", []byte("select * from t"))
	require.Equal(t, 200, res.Status, string(res.Body))

	body := string(res.Body)
	require.True(t, strings.HasPrefix(body, "id,name,score,live,blob,missing|"), body)
	require.Contains(t, body, "integer:7", "a number is a number")
	require.Contains(t, body, "text:alice")
	require.Contains(t, body, "real:1.5")
	require.Contains(t, body, "boolean:true")
	require.Contains(t, body, "blob:3", "three bytes")
	require.Contains(t, body, "null", "and a null column is not an empty string")

	statements, _ := db.seen()
	require.Equal(t, []string{"select * from t"}, statements)
}

func TestAComponentExecs(t *testing.T) {
	db := &server{affected: 3}
	instance := start(t, tier(t, cacher(t), wasm.WithDatabases(db)), invoke.Spec{
		Name: "fn", Reaches: dbGrant("ledger", "h", "exec"),
	})

	res := call(t, instance, "/exec/ledger/", []byte("delete from t"))
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "3", string(res.Body), "the row count came back")
}

// TestEveryArgumentCaseIsBoundAsITSELF. Binding a number as text is how a query
// stops using an index, and binding "" where the author meant NULL writes a row
// that matches neither `IS NULL` nor a NOT NULL constraint.
func TestEveryArgumentCrossesAsItsOwnType(t *testing.T) {
	db := &server{affected: 1}
	instance := start(t, tier(t, cacher(t), wasm.WithDatabases(db)), invoke.Spec{
		Name: "fn", Reaches: dbGrant("ledger", "h", "exec"),
	})

	res := call(t, instance, "/bind/ledger/", nil)
	require.Equal(t, 200, res.Status, string(res.Body))

	_, bound := db.seen()
	require.Len(t, bound, 1)
	require.Equal(t, []any{
		nil, "hello", int64(-42), 1.5, true, []byte{1, 2, 3},
	}, bound[0])
}

// ── the grant is the authorization ───────────────────────────────────────────

func TestAnUndeclaredDatabaseIsRefused(t *testing.T) {
	db := &server{}
	instance := start(t, tier(t, cacher(t), wasm.WithDatabases(db)), invoke.Spec{
		Name: "fn", Reaches: dbGrant("ledger", "h", "query"),
	})

	res := call(t, instance, "/query/somebody-elses/", []byte("select 1"))
	require.Equal(t, 500, res.Status)
	require.Equal(t, "no-such-database:somebody-elses", string(res.Body))

	statements, _ := db.seen()
	require.Empty(t, statements, "nothing reached the server")
}

// TestQueryDoesNotBuyExec, which is the whole reason the vocabulary calls exec
// a write: the host cannot classify arbitrary SQL, so an author who asked for
// reads gets reads.
func TestAReadGrantCannotWrite(t *testing.T) {
	db := &server{}
	instance := start(t, tier(t, cacher(t), wasm.WithDatabases(db)), invoke.Spec{
		Name: "fn", Reaches: dbGrant("ledger", "h", "query"),
	})

	res := call(t, instance, "/exec/ledger/", []byte("delete from t"))
	require.Equal(t, 500, res.Status)
	require.Equal(t, "denied:ledger cannot exec", string(res.Body))

	statements, _ := db.seen()
	require.Empty(t, statements, "the delete never reached the server")
}

func TestARefusedDatabaseCarriesItsReason(t *testing.T) {
	db := &server{}
	instance := start(t, tier(t, cacher(t), wasm.WithDatabases(db)), invoke.Spec{
		Name: "fn",
		Reaches: []invoke.Grant{{
			Kind: "database", Label: "ledger", Handle: "h", Ops: []string{"query"},
			Settings: map[string]string{"type": "sql"},
			Refused:  `database "ledger" is tau's own sql engine, which this cloud does not ship`,
		}},
	})

	res := call(t, instance, "/query/ledger/", []byte("select 1"))
	require.Equal(t, 500, res.Status)
	require.Contains(t, string(res.Body), "does not ship")
}

// ── the two failures, told apart ─────────────────────────────────────────────

// TestAStatementTheServerRefusedIsNotAnOutage. A caller retries one and fixes
// the other; folding them together has every syntax error retried to a timeout.
func TestARefusedStatementIsItsOwnArm(t *testing.T) {
	db := &server{err: fmt.Errorf("%w: syntax error at or near \"slect\"", wasm.ErrStatementFailed)}
	instance := start(t, tier(t, cacher(t), wasm.WithDatabases(db)), invoke.Spec{
		Name: "fn", Reaches: dbGrant("ledger", "h", "query"),
	})

	res := call(t, instance, "/query/ledger/", []byte("slect 1"))
	require.Equal(t, 500, res.Status)
	require.Contains(t, string(res.Body), "failed:")
	require.Contains(t, string(res.Body), "syntax error")
}

func TestAnUnreachableServerIsUnavailable(t *testing.T) {
	db := &server{err: errors.New("dial tcp: connection refused")}
	instance := start(t, tier(t, cacher(t), wasm.WithDatabases(db)), invoke.Spec{
		Name: "fn", Reaches: dbGrant("ledger", "h", "query"),
	})

	res := call(t, instance, "/query/ledger/", []byte("select 1"))
	require.Equal(t, 500, res.Status)
	require.Contains(t, string(res.Body), "unavailable:")
	require.Contains(t, string(res.Body), "connection refused")
}

// ── when there is nothing behind the grant ───────────────────────────────────

func TestATierWithNoDatabasesRefusesAWorkloadThatNeedsOne(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: dbGrant("ledger", "h", "query"),
	})
	require.ErrorIs(t, err, wasm.ErrNoDatabases)
}

func TestATierWithNoDatabasesRunsAWorkloadThatNeedsNone(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn"})

	res := call(t, instance, "/query/ledger/", []byte("select 1"))
	require.Equal(t, 500, res.Status)
	require.Equal(t, "no-such-database:ledger", string(res.Body))

	// Still alive, which is the half an unprovided import would have taken.
	require.Equal(t, 500, call(t, instance, "/query/other/", nil).Status)
}

// TestAllThreeCapabilitiesInOneComponent, because they are registered together
// and a component that uses one must not lose the others.
func TestACacheAStreamAndADatabaseInOneWorkload(t *testing.T) {
	backing := newCaches("QmCache")
	sent := newPublisher()
	db := &server{affected: 1}

	instance := start(t, tier(t, cacher(t),
		wasm.WithCaches(backing), wasm.WithStreams(sent), wasm.WithDatabases(db)),
		invoke.Spec{
			Name: "fn",
			Reaches: []invoke.Grant{
				{Kind: "cache", Label: "sessions", Handle: "QmCache", Ops: []string{"get", "put"}},
				{Kind: "stream", Label: "orders", Handle: "QmOrders", Ops: []string{"publish"}},
				{Kind: "database", Label: "ledger", Handle: "QmLedger", Ops: []string{"exec"},
					Settings: map[string]string{"type": "sql"}},
			},
		})

	require.Equal(t, 200, call(t, instance, "/roundtrip/sessions/k", []byte("v")).Status)
	require.Equal(t, 204, call(t, instance, "/publish/orders/", []byte("m")).Status)
	require.Equal(t, 200, call(t, instance, "/exec/ledger/", []byte("insert")).Status)

	statements, _ := db.seen()
	require.Equal(t, []string{"insert"}, statements)
	require.Len(t, sent.messages("QmOrders"), 1)
}

// ── the kv data model ────────────────────────────────────────────────────────

// keyValues is a kv database, as this tier sees one.
type keyValues struct {
	mu     sync.Mutex
	values map[string][]byte
	err    error
	opened int
}

func newKeyValues() *keyValues { return &keyValues{values: map[string][]byte{}} }

func (s *keyValues) Open(_ context.Context, _ invoke.Grant) (wasm.KV, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opened++
	if s.err != nil {
		return nil, s.err
	}
	return s, nil
}

func (s *keyValues) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", wasm.ErrNotFound, key)
	}
	return v, nil
}

func (s *keyValues) Put(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *keyValues) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

func (s *keyValues) Keys(_ context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.values {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

func kvGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{
		Kind: "database", Label: label, Handle: handle, Ops: ops,
		Settings: map[string]string{"type": "kv", "kv/engine": "distributed"},
	}}
}

// TestAGuestReadsAndWritesAKvDatabase — tau's own engine, which is the one a
// project gets with nothing to provision.
func TestAComponentUsesAKvDatabase(t *testing.T) {
	db := newKeyValues()
	instance := start(t, tier(t, cacher(t), wasm.WithKV(db)), invoke.Spec{
		Name: "fn", Reaches: kvGrant("tokens", "shop/prod/QmTokens", "get", "put", "delete", "list"),
	})

	require.Equal(t, 204, call(t, instance, "/kv-put/tokens/session:1", []byte("alice")).Status)

	res := call(t, instance, "/kv-get/tokens/session:1", nil)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "alice", string(res.Body))

	res = call(t, instance, "/kv-keys/tokens/session:", nil)
	require.Equal(t, 200, res.Status)
	require.Equal(t, "session:1", string(res.Body))

	require.Equal(t, 204, call(t, instance, "/kv-delete/tokens/session:1", nil).Status)

	res = call(t, instance, "/kv-get/tokens/session:1", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body), "a miss is its own arm and not an outage")
}

// TestAKvGrantIsNotASqlGrant, so one label cannot be spent on the other door —
// which matters because both are the `database` kind.
func TestTheDataModelSelectsTheInterface(t *testing.T) {
	db := newKeyValues()
	server := &server{}

	instance := start(t, tier(t, cacher(t), wasm.WithKV(db), wasm.WithDatabases(server)),
		invoke.Spec{
			Name:    "fn",
			Reaches: kvGrant("tokens", "shop/prod/QmTokens", "get", "put"),
		})

	// The sql door does not see a kv database.
	res := call(t, instance, "/query/tokens/", []byte("select 1"))
	require.Equal(t, 500, res.Status)
	require.Equal(t, "no-such-database:tokens", string(res.Body))

	statements, _ := server.seen()
	require.Empty(t, statements)
}

func TestAnUngrantedKvVerbIsDenied(t *testing.T) {
	db := newKeyValues()
	instance := start(t, tier(t, cacher(t), wasm.WithKV(db)), invoke.Spec{
		Name: "fn", Reaches: kvGrant("tokens", "h", "get"),
	})

	res := call(t, instance, "/kv-put/tokens/k", []byte("v"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:tokens cannot put", string(res.Body))
}

// TestWatchBuysNothing: the vocabulary declares it and the store underneath has
// no change feed, so a resource asking for it is told rather than handed a
// channel that never delivers.
func TestAGrantForWatchDoesNotRead(t *testing.T) {
	db := newKeyValues()
	instance := start(t, tier(t, cacher(t), wasm.WithKV(db)), invoke.Spec{
		Name: "fn", Reaches: kvGrant("tokens", "h", "watch"),
	})

	res := call(t, instance, "/kv-get/tokens/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "denied:tokens cannot get", string(res.Body))
}

func TestATierWithNoKvRefusesAWorkloadThatNeedsOne(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: kvGrant("tokens", "h", "get"),
	})
	require.ErrorIs(t, err, wasm.ErrNoKV)
}
