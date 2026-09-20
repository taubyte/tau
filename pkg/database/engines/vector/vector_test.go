package vector_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/database/engines/vector"
)

type memStore struct {
	mu     sync.RWMutex
	values map[string][]byte
	err    error
}

func newStore() *memStore { return &memStore{values: map[string][]byte{}} }

func (m *memStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, false, m.err
	}
	v, ok := m.values[key]
	return v, ok, nil
}

func (m *memStore) Put(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.values[key] = value
	return nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.values, key)
	return nil
}

func (m *memStore) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.err != nil {
		return nil, m.err
	}
	var keys []string
	for k := range m.values {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func spec(metric string, dimensions string) database.Spec {
	options := map[string]string{}
	if dimensions != "" {
		options[vector.OptionDimensions] = dimensions
	}
	if metric != "" {
		options[vector.OptionMetric] = metric
	}
	return database.Spec{
		Type: database.Vector, Engine: vector.Name,
		Project: "shop", Environment: "prod", Name: "knowledge",
		Options: options,
	}
}

func open(t *testing.T, store vector.Store, s database.Spec) database.VectorConn {
	t.Helper()

	engine, err := vector.New(store)
	require.NoError(t, err)

	conn, err := engine.Open(t.Context(), s)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	typed, ok := conn.(database.VectorConn)
	require.True(t, ok, "an engine that claims vector must answer vector's surface")
	return typed
}

func TestAnEngineNeedsAStore(t *testing.T) {
	_, err := vector.New(nil)
	require.ErrorIs(t, err, vector.ErrNoStore)
}

func TestTheEngineSatisfiesTheRegistry(t *testing.T) {
	engine, err := vector.New(newStore())
	require.NoError(t, err)

	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))
	require.Contains(t, registry.EnginesFor(database.Vector), vector.Name)
}

// ── the search ───────────────────────────────────────────────────────────────

// TestTheNEARESTComesBackFirst, which is the whole of what this type does.
func TestSearchRanksByCloseness(t *testing.T) {
	c := open(t, newStore(), spec("", "2"))
	ctx := t.Context()

	require.NoError(t, c.Upsert(ctx, "east", []float32{1, 0}, map[string]any{"where": "east"}))
	require.NoError(t, c.Upsert(ctx, "north", []float32{0, 1}, nil))
	require.NoError(t, c.Upsert(ctx, "west", []float32{-1, 0}, nil))

	found, err := c.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Len(t, found, 3)

	require.Equal(t, "east", found[0].ID, "the same direction is closest")
	require.InDelta(t, 1.0, found[0].Score, 0.0001)
	require.Equal(t, "north", found[1].ID, "orthogonal is next")
	require.Equal(t, "west", found[2].ID, "opposite is furthest")
	require.Equal(t, map[string]any{"where": "east"}, found[0].Meta, "and the meta comes back")
}

// TestKBoundsTheAnswer, and k<=0 is everything.
func TestKTakesTheTop(t *testing.T) {
	c := open(t, newStore(), spec("", "2"))
	ctx := t.Context()

	for id, v := range map[string][]float32{"a": {1, 0}, "b": {0.9, 0.1}, "c": {-1, 0}} {
		require.NoError(t, c.Upsert(ctx, id, v, nil))
	}

	found, err := c.Search(ctx, []float32{1, 0}, 2)
	require.NoError(t, err)
	require.Len(t, found, 2)
	require.Equal(t, "a", found[0].ID)

	all, err := c.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Len(t, all, 3, "k<=0 is everything")
}

// TestEveryMetricIsServed, and each ranks by what it measures.
func TestTheThreeMetrics(t *testing.T) {
	ctx := context.Background()

	// COSINE IGNORES MAGNITUDE AND DOT DOES NOT, which is the difference an
	// author picks between — and the fixture has to separate them:
	//
	//	query {1,0}
	//	close {1,0.1}   nearly the same direction, small
	//	long  {10,5}    further off direction, large
	//
	// cosine: close 0.995 beats long 0.894. dot: long 10 beats close 1.
	// euclidean: close is 0.1 away and long is 10.3.
	for _, c := range []struct {
		metric string
		first  string
	}{
		{vector.Cosine, "close"},
		{vector.Dot, "long"},
		{vector.Euclidean, "close"},
	} {
		conn := open(t, newStore(), spec(c.metric, "2"))
		require.NoError(t, conn.Upsert(ctx, "close", []float32{1, 0.1}, nil))
		require.NoError(t, conn.Upsert(ctx, "long", []float32{10, 5}, nil))

		found, err := conn.Search(ctx, []float32{1, 0}, 1)
		require.NoError(t, err)
		require.Equal(t, c.first, found[0].ID, "metric %s", c.metric)
	}
}

// TestCosineTIESForOneDIRECTION, whatever the magnitude — which is the property
// the test above had to work around, and is worth stating on its own.
func TestCosineIgnoresMagnitudeEntirely(t *testing.T) {
	c := open(t, newStore(), spec(vector.Cosine, "2"))
	ctx := t.Context()

	require.NoError(t, c.Upsert(ctx, "unit", []float32{1, 0}, nil))
	require.NoError(t, c.Upsert(ctx, "ten", []float32{10, 0}, nil))

	found, err := c.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Len(t, found, 2)
	require.Equal(t, found[0].Score, found[1].Score, "one direction, one score")
	require.InDelta(t, 1.0, found[0].Score, 0.0001)
}

// TestEuclideanScoresAreNegativeDistances, which is the one surprising number
// and is what keeps ordering and reading a score under one rule.
func TestEuclideanScoreIsNegatedDistance(t *testing.T) {
	c := open(t, newStore(), spec(vector.Euclidean, "2"))
	ctx := t.Context()

	require.NoError(t, c.Upsert(ctx, "here", []float32{0, 0}, nil))
	require.NoError(t, c.Upsert(ctx, "three", []float32{3, 4}, nil))

	found, err := c.Search(ctx, []float32{0, 0}, 0)
	require.NoError(t, err)
	require.Equal(t, "here", found[0].ID)
	require.InDelta(t, 0.0, found[0].Score, 0.0001)
	require.InDelta(t, -5.0, found[1].Score, 0.0001, "3-4-5")
}

// TestAZeroVectorIsNoDistanceFromAnything rather than NaN, which sorts
// unpredictably.
func TestAZeroVectorScoresZeroUnderCosine(t *testing.T) {
	c := open(t, newStore(), spec(vector.Cosine, "2"))
	ctx := t.Context()

	require.NoError(t, c.Upsert(ctx, "zero", []float32{0, 0}, nil))

	found, err := c.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, float32(0), found[0].Score)
}

// TestTiesAreBrokenByIdSoTwoSearchesAgree.
func TestEqualScoresAreOrderedByID(t *testing.T) {
	c := open(t, newStore(), spec("", "2"))
	ctx := t.Context()

	for _, id := range []string{"c", "a", "b"} {
		require.NoError(t, c.Upsert(ctx, id, []float32{1, 0}, nil))
	}

	for range 5 {
		found, err := c.Search(ctx, []float32{1, 0}, 0)
		require.NoError(t, err)
		require.Equal(t, []string{"a", "b", "c"}, []string{found[0].ID, found[1].ID, found[2].ID})
	}
}

// ── the width ────────────────────────────────────────────────────────────────

// TestTheWidthIsENFORCED: a 1536-wide query against 768-wide vectors is not a
// bad answer, it is a meaningless one.
func TestAVectorOfAnotherWidthIsRefused(t *testing.T) {
	c := open(t, newStore(), spec("", "3"))
	ctx := t.Context()

	require.ErrorIs(t, c.Upsert(ctx, "a", []float32{1, 0}, nil), vector.ErrWidth)
	require.NoError(t, c.Upsert(ctx, "a", []float32{1, 0, 0}, nil))

	_, err := c.Search(ctx, []float32{1, 0}, 0)
	require.ErrorIs(t, err, vector.ErrWidth)
}

// TestAStoredVectorOfAnotherWidthIsNotSCORED, because scoring it against a
// truncated copy would rank a wrong answer above a right one.
func TestAWrongWidthRowIsSkipped(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("", "2"))
	ctx := t.Context()

	require.NoError(t, c.Upsert(ctx, "good", []float32{1, 0}, nil))
	require.NoError(t, store.Put(ctx, "shop/prod/vector/knowledge/wide", []byte(`{"embedding":[1,0,0,0]}`)))
	require.NoError(t, store.Put(ctx, "shop/prod/vector/knowledge/junk", []byte("not json")))

	found, err := c.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "good", found[0].ID)
}

func TestADatabaseWithNoWidthIsRefused(t *testing.T) {
	ctx := context.Background()

	for _, width := range []string{"", "0", "-1", "wide"} {
		c := open(t, newStore(), spec("", width))
		err := c.Upsert(ctx, "a", []float32{1}, nil)
		require.ErrorIs(t, err, vector.ErrNoDimension, "width %q", width)
	}
}

func TestAnUnknownMetricIsRefusedByName(t *testing.T) {
	c := open(t, newStore(), spec("jaccard", "2"))

	err := c.Upsert(t.Context(), "a", []float32{1, 0}, nil)
	require.ErrorIs(t, err, vector.ErrNoMetric)
	require.ErrorContains(t, err, "jaccard")
}

// ── the rest ─────────────────────────────────────────────────────────────────

func TestUpsertReplaces(t *testing.T) {
	c := open(t, newStore(), spec("", "2"))
	ctx := t.Context()

	require.NoError(t, c.Upsert(ctx, "a", []float32{1, 0}, map[string]any{"v": 1.0}))
	require.NoError(t, c.Upsert(ctx, "a", []float32{0, 1}, map[string]any{"v": 2.0}))

	found, err := c.Search(ctx, []float32{0, 1}, 0)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, map[string]any{"v": 2.0}, found[0].Meta)
}

func TestDelete(t *testing.T) {
	c := open(t, newStore(), spec("", "2"))
	ctx := t.Context()

	require.NoError(t, c.Upsert(ctx, "a", []float32{1, 0}, nil))
	require.NoError(t, c.Delete(ctx, "a"))

	found, err := c.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Empty(t, found)

	require.NoError(t, c.Delete(ctx, "a"), "deleting what is gone succeeds")
}

func TestAnEmptyIdIsRefused(t *testing.T) {
	c := open(t, newStore(), spec("", "2"))
	require.ErrorIs(t, c.Upsert(t.Context(), "", []float32{1, 0}, nil), vector.ErrNoID)
	require.ErrorIs(t, c.Delete(t.Context(), ""), vector.ErrNoID)
}

func TestEachDatabaseIsItsOwnCollection(t *testing.T) {
	store := newStore()
	ctx := context.Background()

	mine := spec("", "2")
	theirs := spec("", "2")
	theirs.Name = "other"

	a := open(t, store, mine)
	b := open(t, store, theirs)

	require.NoError(t, a.Upsert(ctx, "x", []float32{1, 0}, map[string]any{"where": "mine"}))
	require.NoError(t, b.Upsert(ctx, "x", []float32{1, 0}, map[string]any{"where": "theirs"}))

	found, err := a.Search(ctx, []float32{1, 0}, 0)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "mine", found[0].Meta["where"])
}

func TestASeparatorInANameIsRefused(t *testing.T) {
	engine, err := vector.New(newStore())
	require.NoError(t, err)

	bad := spec("", "2")
	bad.Name = "../production/knowledge"

	conn, err := engine.Open(context.Background(), bad)
	require.NoError(t, err, "the registry's probe is address-less")

	require.ErrorIs(t, conn.(database.VectorConn).Upsert(
		context.Background(), "a", []float32{1, 0}, nil), database.ErrSeparator)
}

func TestAStoreFailureIsReported(t *testing.T) {
	store := newStore()
	c := open(t, store, spec("", "2"))
	store.err = errors.New("the swarm is unreachable")

	require.ErrorIs(t, c.Upsert(t.Context(), "a", []float32{1, 0}, nil), store.err)
	_, err := c.Search(t.Context(), []float32{1, 0}, 0)
	require.ErrorIs(t, err, store.err)
}

func TestTheWrongTypeIsRefused(t *testing.T) {
	engine, err := vector.New(newStore())
	require.NoError(t, err)

	_, err = engine.Open(context.Background(), database.Spec{
		Type: database.KV, Project: "shop", Environment: "prod", Name: "knowledge",
	})
	require.ErrorContains(t, err, "backs vector")
}
