package database

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
)

// counting is an engine that answers with a connection remembering whether it
// was closed, so the table's promises are observable.
type counting struct {
	mu     sync.Mutex
	opened int
	live   int

	// refuse makes every later Open fail. It is set AFTER registration because
	// Registry.Register opens a probe connection to prove the engine backs the
	// type it claims, so an engine that never opens cannot be registered.
	refuse error
}

// counted is a real kv over a map, because Registry.Register opens a probe and
// refuses an engine whose connection does not answer its type's operations.
type counted struct {
	e      *counting
	closed bool

	mu   sync.Mutex
	rows map[string][]byte
}

func (c *counted) Close() error {
	c.e.mu.Lock()
	defer c.e.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.e.live--
	}
	return nil
}

func (c *counted) Get(_ context.Context, key string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, held := c.rows[key]
	if !held {
		return nil, kvengine.ErrNotFound
	}
	return value, nil
}

func (c *counted) Put(_ context.Context, key string, value []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows[key] = value
	return nil
}

func (c *counted) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.rows, key)
	return nil
}

func (c *counted) List(_ context.Context, prefix string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := []string{}
	for key := range c.rows {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (c *counted) Watch(context.Context, string) (<-chan string, error) {
	return nil, errors.New("counting: no watch")
}

// NAMED FOR THE VOCABULARY'S ENGINE, because the registry is keyed by (type,
// engine) and the engine is what the author wrote: `kv: {engine: distributed}`.
// A fixture named after itself is one the registry can never be asked for.
// NAMED FOR THE ENGINE THE RESOLVER ASKS FOR, which is not quite what the author
// wrote: the vocabulary says `engine: distributed` on both consensus arms, and
// declarations.go turns the declared resolution into the engine name the
// registry is keyed by.
func (c *counting) Name() string           { return servedConsensus["crdt"] }
func (c *counting) Types() []database.Type { return []database.Type{database.KV} }

func (c *counting) Open(context.Context, database.Spec) (database.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refuse != nil {
		return nil, c.refuse
	}
	c.opened++
	c.live++
	return &counted{e: c, rows: map[string][]byte{}}, nil
}

func (c *counting) counts() (opened, live int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opened, c.live
}

func residing(t *testing.T, now func() time.Time, max int, idle time.Duration) (*engines, *counting) {
	t.Helper()

	engine := &counting{}
	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))

	// REGISTRATION OPENS A PROBE, because Register proves an engine answers its
	// type's operations rather than trusting it to. That open is not one of
	// this table's, so the counters start from after it.
	engine.mu.Lock()
	engine.opened, engine.live = 0, 0
	engine.mu.Unlock()

	return newEngines(registry, now, max, idle), engine
}

func spec(name string) database.Spec {
	return database.Spec{Type: database.KV, Engine: servedConsensus["crdt"], Project: "shop", Environment: "prod", Name: name}
}

// TestADatabaseIsOpenedOnceAndKeptWarm, which is the whole reason this service
// owns the engines rather than the worker.
func TestADatabaseIsOpenedOnceAndKeptWarm(t *testing.T) {
	now := time.Unix(0, 0)
	e, engine := residing(t, func() time.Time { return now }, 8, time.Minute)

	for range 5 {
		_, err := e.open(t.Context(), "shop/prod/db", 1, spec("db"))
		require.NoError(t, err)
	}

	opened, live := engine.counts()
	require.Equal(t, 1, opened, "the engine was rebuilt for a request that could have used it")
	require.Equal(t, 1, live)
	require.Equal(t, 1, e.held())
}

// TestANewerDeploymentReopens.
//
// This is the one v1 never did. A resource id is stable across an edit, so a
// changed host or a rotated credential arrives under the SAME key — and a table
// that answered from what it already held served the connection the author
// replaced, until the node restarted.
func TestANewerDeploymentReopens(t *testing.T) {
	now := time.Unix(0, 0)
	e, engine := residing(t, func() time.Time { return now }, 8, time.Minute)

	first, err := e.open(t.Context(), "shop/prod/db", 1, spec("db"))
	require.NoError(t, err)

	second, err := e.open(t.Context(), "shop/prod/db", 2, spec("db"))
	require.NoError(t, err)
	require.NotSame(t, first, second, "the rotated credential was ignored")

	opened, _ := engine.counts()
	require.Equal(t, 2, opened)
	require.Equal(t, 1, e.held(), "and the one it replaced is not still held")

	require.Eventually(t, func() bool {
		_, live := engine.counts()
		return live == 1
	}, time.Second, 10*time.Millisecond, "the replaced connection was leaked")
}

// TestADatabaseNobodyAsksForIsLetGo. Holding it costs a connection on somebody
// else's server, and the request that needs it again can afford to open it.
func TestADatabaseNobodyAsksForIsLetGo(t *testing.T) {
	now := time.Unix(0, 0)
	e, engine := residing(t, func() time.Time { return now }, 8, time.Minute)

	_, err := e.open(t.Context(), "shop/prod/idle", 1, spec("idle"))
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)

	_, err = e.open(t.Context(), "shop/prod/busy", 1, spec("busy"))
	require.NoError(t, err)

	require.Equal(t, 1, e.held(), "the idle one is still open")
	require.Eventually(t, func() bool {
		_, live := engine.counts()
		return live == 1
	}, time.Second, 10*time.Millisecond)
}

// TestTheTableIsBounded, which an unbounded one is not — and unbounded is what
// v1 shipped.
func TestTheTableIsBounded(t *testing.T) {
	now := time.Unix(0, 0)
	e, _ := residing(t, func() time.Time { return now }, 2, time.Hour)

	for _, name := range []string{"a", "b", "c", "d"} {
		_, err := e.open(t.Context(), "shop/prod/"+name, 1, spec(name))
		require.NoError(t, err)
		now = now.Add(time.Second)
	}

	require.LessOrEqual(t, e.held(), 2)
}

// TestClosingReleasesEverything.
func TestClosingReleasesEverything(t *testing.T) {
	now := time.Unix(0, 0)
	e, engine := residing(t, func() time.Time { return now }, 8, time.Hour)

	for _, name := range []string{"a", "b"} {
		_, err := e.open(t.Context(), "shop/prod/"+name, 1, spec(name))
		require.NoError(t, err)
	}

	require.NoError(t, e.closeAll())
	require.Zero(t, e.held())

	_, live := engine.counts()
	require.Zero(t, live, "a connection outlived the service")
}

// TestAnEngineThatWillNotOpenIsNotRemembered, so one bad moment is not a
// database this node can never serve again.
func TestAnEngineThatWillNotOpenIsNotRemembered(t *testing.T) {
	e, engine := residing(t, nil, 8, time.Hour)

	engine.mu.Lock()
	engine.refuse = errors.New("no route to host")
	engine.mu.Unlock()

	_, err := e.open(t.Context(), "shop/prod/db", 1, spec("db"))
	require.ErrorContains(t, err, "no route to host")
	require.Zero(t, e.held())

	// AND THE NEXT CALL TRIES AGAIN. Remembering the failure would turn one
	// unreachable moment into a database this node refuses forever.
	engine.mu.Lock()
	engine.refuse = nil
	engine.mu.Unlock()

	_, err = e.open(t.Context(), "shop/prod/db", 1, spec("db"))
	require.NoError(t, err)
	require.Equal(t, 1, e.held())
}
