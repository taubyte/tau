package secrets

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/peer/peertest"
)

type manualClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func TestTheReplacedVersionIsServedUntilItsRetentionEnds(t *testing.T) {
	mn := peertest.Net(t)
	clock := &manualClock{at: time.Now()}
	srv, client, _ := single(t, mn, withClock(clock.now), withReplacedRetention(time.Hour))
	ctx := t.Context()
	r := Ref{Scope: Project("p", "e"), Name: "hook"}

	require.NoError(t, client.Set(ctx, r, []byte("one")))
	_, _, err := client.Previous(ctx, r)
	require.ErrorIs(t, err, ErrNotSet, "a secret written once has no previous version")

	clock.advance(time.Minute)
	require.NoError(t, client.Set(ctx, r, []byte("two")))
	clock.advance(time.Minute)
	require.NoError(t, client.Set(ctx, r, []byte("three")))
	replaced := clock.now()

	got, at, err := client.Previous(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, "two", string(got))
	assert.Equal(t, replaced.UnixNano(), at.UnixNano())

	require.NoError(t, srv.reconcile(ctx))
	assert.Equal(t, 2, versionsOn(t, ctx, srv, r), "only the immediate predecessor is retained")

	clock.advance(time.Hour)
	require.NoError(t, srv.reconcile(ctx))
	assert.Equal(t, 1, versionsOn(t, ctx, srv, r))
	_, _, err = client.Previous(ctx, r)
	require.ErrorIs(t, err, ErrNotSet)

	value, err := client.Get(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, "three", string(value))
}

func TestPreviousRefusesAnInvalidRef(t *testing.T) {
	mn := peertest.Net(t)
	_, client, _ := single(t, mn)
	_, _, err := client.Previous(t.Context(), Ref{Scope: Project("p", "e"), Name: "a/b"})
	require.ErrorIs(t, err, ErrInvalidRef)
}

func TestNoRetentionIsNegative(t *testing.T) {
	require.Error(t, withReplacedRetention(-time.Second)(&options{}))
}
