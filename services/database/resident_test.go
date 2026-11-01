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

// accounting is an engine counting every connection it opens and how many are live,
// which is what makes the table's promises assertable.
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

// forgetProbe drops the registration probe (pkg/database opens one per type) from the
// count.
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

// The registry is keyed by (type, engine), and the engine is what the author wrote.
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

	// Registration opens a probe; counters start after it.
	engine.mu.Lock()
	engine.opened, engine.live = 0, 0
	engine.mu.Unlock()

	return newEngines(registry, now, max, idle), engine
}

func spec(name string) database.Spec {
	return database.Spec{Type: database.KV, Engine: "distributed", Project: "shop", Environment: "prod", Name: name}
}

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
