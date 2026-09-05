package wasm_test

import (
	"context"
	"encoding/json"
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

func (s *keyValues) OpenKV(_ context.Context, _ invoke.Grant) (wasm.KV, error) {
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

// ── the document data model ──────────────────────────────────────────────────

// collection is a document database, as this tier sees one.
type collection struct {
	mu     sync.Mutex
	docs   map[string][]byte
	err    error
	filter []string
}

func newCollection() *collection { return &collection{docs: map[string][]byte{}} }

func (c *collection) OpenDocuments(_ context.Context, _ invoke.Grant) (wasm.Documents, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c, nil
}

func (c *collection) Get(_ context.Context, id string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	doc, ok := c.docs[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", wasm.ErrNotFound, id)
	}
	return doc, nil
}

func (c *collection) Put(_ context.Context, id string, doc []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !json.Valid(doc) {
		return fmt.Errorf("%w: not json", wasm.ErrMalformed)
	}
	c.docs[id] = doc
	return nil
}

func (c *collection) Delete(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.docs, id)
	return nil
}

func (c *collection) Find(_ context.Context, filter []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filter = append(c.filter, string(filter))

	ids := make([]string, 0, len(c.docs))
	for id := range c.docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := []json.RawMessage{}
	for _, id := range ids {
		out = append(out, c.docs[id])
	}
	return json.Marshal(out)
}

func (c *collection) filters() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.filter...)
}

func docGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{
		Kind: "database", Label: label, Handle: handle, Ops: ops,
		Settings: map[string]string{"type": "document", "document/engine": "distributed"},
	}}
}

// TestAGuestStoresAndFindsDocuments — the document crosses as JSON, which is
// what keeps this interface stable while its shape is the author's business.
func TestAComponentUsesADocumentDatabase(t *testing.T) {
	docs := newCollection()
	instance := start(t, tier(t, cacher(t), wasm.WithDocuments(docs)), invoke.Spec{
		Name: "fn", Reaches: docGrant("orders", "shop/prod/QmOrders", "get", "put", "delete", "query"),
	})

	require.Equal(t, 204, call(t, instance, "/doc-put/orders/order:1",
		[]byte(`{"status":"paid","total":12.5,"address":{"city":"algiers"}}`)).Status)

	res := call(t, instance, "/doc-get/orders/order:1", nil)
	require.Equal(t, 200, res.Status, string(res.Body))
	require.JSONEq(t, `{"status":"paid","total":12.5,"address":{"city":"algiers"}}`, string(res.Body),
		"nested and heterogeneous, unchanged")

	res = call(t, instance, "/doc-find/orders/", []byte(`{"status":"paid"}`))
	require.Equal(t, 200, res.Status, string(res.Body))
	require.JSONEq(t, `[{"status":"paid","total":12.5,"address":{"city":"algiers"}}]`, string(res.Body))
	require.Equal(t, []string{`{"status":"paid"}`}, docs.filters(), "the filter crossed as written")

	require.Equal(t, 204, call(t, instance, "/doc-delete/orders/order:1", nil).Status)

	res = call(t, instance, "/doc-get/orders/order:1", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "not-found", string(res.Body))
}

// TestAMalformedDocumentIsItsOwnArm, because a caller fixes it and does not
// retry it.
func TestABadDocumentIsMalformedAndNotAnOutage(t *testing.T) {
	docs := newCollection()
	instance := start(t, tier(t, cacher(t), wasm.WithDocuments(docs)), invoke.Spec{
		Name: "fn", Reaches: docGrant("orders", "h", "put"),
	})

	res := call(t, instance, "/doc-put/orders/1", []byte("not json at all"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "malformed:")
}

// TestAnEmptyFilterIsEverything, and an empty BODY is an empty filter.
func TestAnEmptyFilterCrossesAsEmpty(t *testing.T) {
	docs := newCollection()
	instance := start(t, tier(t, cacher(t), wasm.WithDocuments(docs)), invoke.Spec{
		Name: "fn", Reaches: docGrant("orders", "h", "query"),
	})

	res := call(t, instance, "/doc-find/orders/", nil)
	require.Equal(t, 200, res.Status)
	require.JSONEq(t, `[]`, string(res.Body), "an empty array and not null")
	require.Equal(t, []string{""}, docs.filters())
}

// TestThreeDataModelsAreThreeDoors, all under the `database` kind.
func TestEachDataModelIsItsOwnDoor(t *testing.T) {
	docs := newCollection()
	kv := newKeyValues()
	sql := &server{}

	instance := start(t, tier(t, cacher(t),
		wasm.WithDocuments(docs), wasm.WithKV(kv), wasm.WithDatabases(sql)),
		invoke.Spec{
			Name:    "fn",
			Reaches: docGrant("orders", "h", "get", "put", "query"),
		})

	// It is a document database, so neither of the other two doors sees it.
	res := call(t, instance, "/kv-get/orders/k", nil)
	require.Equal(t, 404, res.Status)
	require.Equal(t, "no-such-database:orders", string(res.Body))

	res = call(t, instance, "/query/orders/", []byte("select 1"))
	require.Equal(t, 500, res.Status)
	require.Equal(t, "no-such-database:orders", string(res.Body))

	// And its own door works.
	require.Equal(t, 204, call(t, instance, "/doc-put/orders/1", []byte(`{"a":1}`)).Status)
}

func TestAnUngrantedDocumentVerbIsDenied(t *testing.T) {
	docs := newCollection()
	instance := start(t, tier(t, cacher(t), wasm.WithDocuments(docs)), invoke.Spec{
		Name: "fn", Reaches: docGrant("orders", "h", "get"),
	})

	res := call(t, instance, "/doc-put/orders/1", []byte(`{"a":1}`))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:orders cannot put", string(res.Body))
}

func TestATierWithNoDocumentsRefusesAWorkloadThatNeedsOne(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: docGrant("orders", "h", "get"),
	})
	require.ErrorIs(t, err, wasm.ErrNoDocuments)
}

// ── the vector data model ────────────────────────────────────────────────────

// index is a vector database, as this tier sees one.
type index struct {
	mu       sync.Mutex
	vectors  map[string][]float32
	meta     map[string][]byte
	err      error
	searched [][]float32
}

func newIndex() *index {
	return &index{vectors: map[string][]float32{}, meta: map[string][]byte{}}
}

func (i *index) OpenVectors(_ context.Context, _ invoke.Grant) (wasm.Vectors, error) {
	if i.err != nil {
		return nil, i.err
	}
	return i, nil
}

func (i *index) Upsert(_ context.Context, id string, embedding []float32, meta []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(embedding) != 2 {
		return fmt.Errorf("%w: %d against 2", wasm.ErrWrongWidth, len(embedding))
	}
	i.vectors[id] = embedding
	i.meta[id] = meta
	return nil
}

func (i *index) Delete(_ context.Context, id string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.vectors, id)
	return nil
}

// Search answers by dot product, which is enough to show an ORDER crossed.
func (i *index) Search(_ context.Context, query []float32, k int) ([]wasm.Match, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.searched = append(i.searched, query)

	if len(query) != 2 {
		return nil, fmt.Errorf("%w: %d against 2", wasm.ErrWrongWidth, len(query))
	}

	out := make([]wasm.Match, 0, len(i.vectors))
	for id, v := range i.vectors {
		out = append(out, wasm.Match{
			ID: id, Score: v[0]*query[0] + v[1]*query[1], Meta: i.meta[id],
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	return out, nil
}

func (i *index) queries() [][]float32 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([][]float32(nil), i.searched...)
}

func vecGrant(label, handle string, ops ...string) []invoke.Grant {
	return []invoke.Grant{{
		Kind: "database", Label: label, Handle: handle, Ops: ops,
		Settings: map[string]string{
			"type": "vector", "vector/engine": "distributed",
			"vector/dimensions": "2", "vector/metric": "cosine",
		},
	}}
}

// TestAGuestUpsertsAndSearches — an embedding crosses as list<f32> in both
// directions, which is the one place a number list is the natural shape.
func TestAComponentSearchesAVectorDatabase(t *testing.T) {
	idx := newIndex()
	instance := start(t, tier(t, cacher(t), wasm.WithVectors(idx)), invoke.Spec{
		Name: "fn", Reaches: vecGrant("knowledge", "shop/prod/QmK", "search", "upsert", "delete"),
	})

	require.Equal(t, 204, call(t, instance, "/vec-upsert/knowledge/east", []byte("1,0")).Status)
	require.Equal(t, 204, call(t, instance, "/vec-upsert/knowledge/north", []byte("0,1")).Status)

	res := call(t, instance, "/vec-search/knowledge/0", []byte("1,0"))
	require.Equal(t, 200, res.Status, string(res.Body))
	require.Equal(t, "east:1.000:,north:0.000:", string(res.Body),
		"ordered by score, with the id and the meta beside it")

	// THE QUERY CROSSED AS FLOATS, not as text somebody parsed twice.
	require.Equal(t, [][]float32{{1, 0}}, idx.queries())

	require.Equal(t, 204, call(t, instance, "/vec-delete/knowledge/east", nil).Status)
	res = call(t, instance, "/vec-search/knowledge/0", []byte("1,0"))
	require.Equal(t, "north:0.000:", string(res.Body))
}

// TestKBoundsWhatComesBack.
func TestSearchHonoursK(t *testing.T) {
	idx := newIndex()
	instance := start(t, tier(t, cacher(t), wasm.WithVectors(idx)), invoke.Spec{
		Name: "fn", Reaches: vecGrant("knowledge", "h", "search", "upsert"),
	})

	for _, id := range []string{"a", "b", "c"} {
		require.Equal(t, 204, call(t, instance, "/vec-upsert/knowledge/"+id, []byte("1,0")).Status)
	}

	res := call(t, instance, "/vec-search/knowledge/2", []byte("1,0"))
	require.Equal(t, 200, res.Status)
	require.Len(t, strings.Split(string(res.Body), ","), 2)
}

// TestTheWrongWidthIsItsOwnArm, because it is a mistake in the CALLER: the
// answer would be meaningless rather than late.
func TestAWrongWidthEmbeddingIsNamed(t *testing.T) {
	idx := newIndex()
	instance := start(t, tier(t, cacher(t), wasm.WithVectors(idx)), invoke.Spec{
		Name: "fn", Reaches: vecGrant("knowledge", "h", "search", "upsert"),
	})

	res := call(t, instance, "/vec-upsert/knowledge/a", []byte("1,0,0"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "wrong-width:")

	res = call(t, instance, "/vec-search/knowledge/0", []byte("1"))
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "wrong-width:")
}

func TestAnUngrantedVectorVerbIsDenied(t *testing.T) {
	idx := newIndex()
	instance := start(t, tier(t, cacher(t), wasm.WithVectors(idx)), invoke.Spec{
		Name: "fn", Reaches: vecGrant("knowledge", "h", "search"),
	})

	res := call(t, instance, "/vec-upsert/knowledge/a", []byte("1,0"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:knowledge cannot upsert", string(res.Body))
}

func TestATierWithNoVectorsRefusesAWorkloadThatNeedsOne(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy", Reaches: vecGrant("knowledge", "h", "search"),
	})
	require.ErrorIs(t, err, wasm.ErrNoVectors)
}

// TestFourDataModelsAreFourDoors, all under the `database` kind — which is the
// whole of what that kind declares.
func TestEveryDataModelIsItsOwnDoor(t *testing.T) {
	idx := newIndex()
	instance := start(t, tier(t, cacher(t),
		wasm.WithVectors(idx), wasm.WithKV(newKeyValues()),
		wasm.WithDocuments(newCollection()), wasm.WithDatabases(&server{})),
		invoke.Spec{
			Name:    "fn",
			Reaches: vecGrant("knowledge", "h", "search", "upsert"),
		})

	// It is a vector database, so none of the other three doors sees it.
	require.Equal(t, "no-such-database:knowledge",
		string(call(t, instance, "/kv-get/knowledge/k", nil).Body))
	require.Equal(t, "no-such-database:knowledge",
		string(call(t, instance, "/doc-get/knowledge/k", nil).Body))
	require.Equal(t, "no-such-database:knowledge",
		string(call(t, instance, "/query/knowledge/", []byte("select 1")).Body))

	// And its own door works.
	require.Equal(t, 204, call(t, instance, "/vec-upsert/knowledge/a", []byte("1,0")).Status)
}
