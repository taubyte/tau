package project_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"
)

// source is a register with a tally and a switch, so a test can say how many
// times a whole compiled project crossed the wire and what happens when it
// cannot.
type source struct {
	mu       sync.Mutex
	reg      *register.Register
	resolves int
	objects  int
	broken   bool
	slow     chan struct{}
}

func newSource(t testing.TB) *source {
	t.Helper()
	r, err := register.New(register.WithStore(register.NewMemStore()))
	require.NoError(t, err)
	return &source{reg: r}
}

func (s *source) publish(t testing.TB, revision uint64, compiled []byte) {
	t.Helper()
	_, err := s.reg.Publish(t.Context(), register.Publication{
		Project: "shop", Environment: "prod", Revision: revision, Compiled: compiled,
	})
	require.NoError(t, err)
}

func (s *source) Resolve(ctx context.Context, p, e string) (register.Pointer, error) {
	s.mu.Lock()
	s.resolves++
	s.mu.Unlock()
	return s.reg.Resolve(ctx, p, e)
}

func (s *source) Object(ctx context.Context, id register.ObjectID) ([]byte, error) {
	s.mu.Lock()
	s.objects++
	broken, slow := s.broken, s.slow
	s.mu.Unlock()

	if slow != nil {
		<-slow
	}
	if broken {
		return nil, errors.New("the register is unreachable")
	}
	return s.reg.Object(ctx, id)
}

func (s *source) counts() (resolves, objects int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolves, s.objects
}

func (s *source) breakIt(broken bool) {
	s.mu.Lock()
	s.broken = broken
	s.mu.Unlock()
}

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// object is a minimal compiled project with one number in it, so two deploys
// differ by content and can be told apart.
func object(memory int) []byte {
	return []byte(`{"object":{"id":"QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR","name":"shop",
		"function":{"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY":{"name":"checkout",
			"execution":{"memory":` + itoa(memory) + `}}}},
		"indexes":{"projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/function/checkout/links":[
			"branches/main/projects/QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"]}}`)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestACompiledProjectIsFetchedOnce(t *testing.T) {
	src := newSource(t)
	src.publish(t, 1, object(32))

	c, err := project.NewCache(src)
	require.NoError(t, err)

	for range 5 {
		p, pointer, err := c.At(t.Context(), "shop", "prod")
		require.NoError(t, err)
		require.Equal(t, uint64(1), pointer.Revision)
		require.Equal(t, "shop", p.Name())
	}

	resolves, objects := src.counts()
	require.Equal(t, 1, objects, "the object crossed the wire once: it is named by its content")
	require.Equal(t, 1, resolves, "and the pointer was read once inside its ttl")
}

// TestADeployTakesEffectWhenTheBudgetIsSpent. That one number is the whole
// promise about how long the old code keeps answering.
func TestADeployTakesEffectWhenTheBudgetIsSpent(t *testing.T) {
	src := newSource(t)
	src.publish(t, 1, object(32))

	now := &clock{at: time.Unix(1_700_000_000, 0)}
	c, err := project.NewCache(src, project.WithTTL(5*time.Second), project.WithClock(now.Now))
	require.NoError(t, err)

	_, first, err := c.At(t.Context(), "shop", "prod")
	require.NoError(t, err)

	src.publish(t, 2, object(64))

	_, again, err := c.At(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, first, again, "inside the budget nothing changed, which is the cost being paid for")

	now.advance(6 * time.Second)

	p, moved, err := c.At(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(2), moved.Revision)

	r, _, err := p.Resource(nil, "function", "checkout")
	require.NoError(t, err)
	mem, _ := r.Int("execution", "memory")
	require.Equal(t, int64(64), mem)
}

// TestOneFetchPerObjectUnderLoad. A deploy makes every in-flight request want
// the same compiled project at the same instant.
func TestOneFetchPerObjectUnderLoad(t *testing.T) {
	src := newSource(t)
	src.publish(t, 1, object(32))
	src.slow = make(chan struct{})

	c, err := project.NewCache(src)
	require.NoError(t, err)

	done := make(chan error, 16)
	for range 16 {
		go func() {
			_, _, err := c.At(t.Context(), "shop", "prod")
			done <- err
		}()
	}

	// Let them all pile up on the one fetch before it answers.
	time.Sleep(50 * time.Millisecond)
	close(src.slow)

	for range 16 {
		require.NoError(t, <-done)
	}

	_, objects := src.counts()
	require.Equal(t, 1, objects)
}

// TestTheCacheIsBounded. Every deploy mints a new content address and the old
// one stays valid forever, so "still correct" is not a bound.
func TestTheCacheIsBounded(t *testing.T) {
	src := newSource(t)
	now := &clock{at: time.Unix(1_700_000_000, 0)}

	c, err := project.NewCache(src, project.WithMax(2), project.WithTTL(time.Nanosecond), project.WithClock(now.Now))
	require.NoError(t, err)

	for revision := range 5 {
		src.publish(t, uint64(revision+1), object(revision+1))
		now.advance(time.Second)

		_, _, err := c.At(t.Context(), "shop", "prod")
		require.NoError(t, err)
	}

	require.LessOrEqual(t, c.Held(), 2)
}

// TestAFailedLoadIsNotRemembered. A failure is usually the register being
// unreachable, and caching it would turn one bad moment into a project that
// never loads again.
func TestAFailedLoadIsNotRemembered(t *testing.T) {
	src := newSource(t)
	src.publish(t, 1, object(32))
	src.breakIt(true)

	c, err := project.NewCache(src)
	require.NoError(t, err)

	_, _, err = c.At(t.Context(), "shop", "prod")
	require.Error(t, err)
	require.Zero(t, c.Held())

	src.breakIt(false)

	_, _, err = c.At(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, 1, c.Held())
}

func TestAnUnpublishedEnvironmentIsNotCached(t *testing.T) {
	src := newSource(t)
	c, err := project.NewCache(src)
	require.NoError(t, err)

	_, _, err = c.At(t.Context(), "shop", "prod")
	require.ErrorIs(t, err, register.ErrNotPublished)

	src.publish(t, 1, object(32))

	_, _, err = c.At(t.Context(), "shop", "prod")
	require.NoError(t, err, "a miss is not remembered either")
}

// TestBytesThatDoNotLoadAreReportedAndNotHeld.
func TestBytesThatDoNotLoadAreReported(t *testing.T) {
	src := newSource(t)
	src.publish(t, 1, []byte("{not a compiled project"))

	c, err := project.NewCache(src)
	require.NoError(t, err)

	_, _, err = c.At(t.Context(), "shop", "prod")
	require.Error(t, err)
	require.Zero(t, c.Held())
}

func TestTheCacheRefusesNonsense(t *testing.T) {
	_, err := project.NewCache(nil)
	require.ErrorIs(t, err, project.ErrNoRegistry)

	src := newSource(t)
	for _, opt := range []project.CacheOption{
		project.WithTTL(-time.Second),
		project.WithMax(0),
		project.WithClock(nil),
	} {
		_, err := project.NewCache(src, opt)
		require.Error(t, err)
	}

	// A zero TTL is legal and means "ask every time", which is a real choice
	// for a node that must never serve replaced code.
	c, err := project.NewCache(src, project.WithTTL(0))
	require.NoError(t, err)

	src.publish(t, 1, object(32))
	for range 3 {
		_, _, err := c.At(t.Context(), "shop", "prod")
		require.NoError(t, err)
	}
	resolves, objects := src.counts()
	require.Equal(t, 3, resolves)
	require.Equal(t, 1, objects, "and the object is still fetched once")
}
