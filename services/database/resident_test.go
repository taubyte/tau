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

// accounting is an engine that accounts for every connection it hands out: how
// many were opened, and how many are still live.
//
// THAT IS WHAT MAKES THE TABLE'S PROMISES ASSERTABLE. "Opened once and kept
// warm", "a newer deployment closes what it replaces", "an idle one is let go"
// and "nothing outlived the service" are all statements about connections
// nobody holds a reference to any more — so the only way to check them is for
// the connections themselves to say.
type accounting struct {
	mu     sync.Mutex
	opened int
	live   int

	// refuse makes every later Open fail. It is set AFTER registration because
	// Registry.Register opens a probe connection to prove the engine backs the
	// type it claims, so an engine that never opens cannot be registered.
	refuse error

	// answers is what every operation on its connections returns instead of
	// doing anything, which is how a fixture stands in for a raft store whose
	// group is led somewhere else or has not formed. Nil is an engine that
	// answers normally.
	answers error
}

// accounted is one of its connections: a real kv over a map, because
// Registry.Register opens a probe and refuses an engine whose connection does
// not answer its type's operations.
type accounted struct {
	e      *accounting
	closed bool

	mu   sync.Mutex
	rows map[string][]byte
}

func (c *accounted) Close() error {
	c.e.mu.Lock()
	defer c.e.mu.Unlock()
	if !c.closed {
		c.closed = true
		c.e.live--
	}
	return nil
}

func (c *accounted) Get(_ context.Context, key string) ([]byte, error) {
	if err := c.answers(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	value, held := c.rows[key]
	if !held {
		return nil, kvengine.ErrNotFound
	}
	return value, nil
}

func (c *accounted) Put(_ context.Context, key string, value []byte) error {
	if err := c.answers(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows[key] = value
	return nil
}

func (c *accounted) Delete(_ context.Context, key string) error {
	if err := c.answers(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.rows, key)
	return nil
}

func (c *accounted) List(_ context.Context, prefix string) ([]string, error) {
	if err := c.answers(); err != nil {
		return nil, err
	}
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

func (c *accounted) Watch(context.Context, string) (<-chan string, error) {
	return nil, errors.New("accounting: no watch")
}

// answers is what this connection refuses with, read from the engine so a test
// can change its mind after the connection is warm.
func (c *accounted) answers() error {
	c.e.mu.Lock()
	defer c.e.mu.Unlock()
	return c.e.answers
}

// forgetProbe drops the registration probe from the count, so what a test
// counts afterwards is the connections the SERVICE asked for. pkg/database
// opens one per type to prove the engine answers its operations, which is not
// an open anything under test decided to make.
func (c *accounting) forgetProbe() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opened = 0
}

// refusing makes every operation on this engine's connections answer err.
func (c *accounting) refusing(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answers = err
}

// NAMED FOR THE VOCABULARY'S ENGINE, because the registry is keyed by (type,
// engine) and the engine is what the author wrote: `kv: {engine: distributed}`.
// A fixture named after itself is one the registry can never be asked for.
func (c *accounting) Name() string           { return "distributed" }
func (c *accounting) Types() []database.Type { return []database.Type{database.KV} }

func (c *accounting) Open(context.Context, database.Spec) (database.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refuse != nil {
		return nil, c.refuse
	}
	c.opened++
	c.live++
	return &accounted{e: c, rows: map[string][]byte{}}, nil
}

// counts is what it has accounted for: opens since the baseline, and
// connections not yet closed.
func (c *accounting) counts() (opened, live int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opened, c.live
}

func residing(t *testing.T, now func() time.Time, max int, idle time.Duration) (*engines, *accounting) {
	t.Helper()

	engine := &accounting{}
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
	return database.Spec{Type: database.KV, Engine: "distributed", Project: "shop", Environment: "prod", Name: name}
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
