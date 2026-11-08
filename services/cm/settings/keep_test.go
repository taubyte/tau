package settings

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
)

// saying is cm answering whatever a test sets.
type saying struct {
	mu     sync.Mutex
	region Region
	err    error
}

func (s *saying) set(r Region, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.region, s.err = r, err
}

func (*saying) Start(context.Context, service.Host) error { return nil }
func (*saying) Close() error                              { return nil }

func (s *saying) Settings(context.Context) (Region, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.region, s.err
}

// beside is an instance serving cm's protocol with src.
type beside struct {
	peer.Node
	*service.Served
}

func (b beside) Peer() peer.Node          { return b.Node }
func (beside) Running() []service.Running { return nil }
func (beside) Config(any) error           { return nil }
func (beside) Root() string               { return "" }
func (beside) Cloud() string              { return "keep-test" }
func (beside) Region() string             { return "" }
func (beside) Now() time.Time             { return time.Now() }

func reading(t *testing.T, src *saying, now *time.Time) *Client {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := beside{Node: p, Served: service.NewServed(p.ID(), nil)}
	h.Serve(Protocol, src)
	c := On(h)
	c.now = func() time.Time { return *now }
	t.Cleanup(func() { c.Close() })
	return c
}

func TestAnAnswerIsKeptForRecheckThenAskedAgain(t *testing.T) {
	src := &saying{region: Region{StorageReplicas: 3}}
	now := time.Unix(1_700_000_000, 0)
	c := reading(t, src, &now)

	got, err := c.Settings(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, got.StorageReplicas)

	src.set(Region{StorageReplicas: 5}, nil)
	now = now.Add(Recheck - time.Second)
	got, err = c.Settings(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, got.StorageReplicas, "an answer was asked again before it was Recheck old")

	now = now.Add(2 * time.Second)
	got, err = c.Settings(t.Context())
	require.NoError(t, err)
	require.Equal(t, 5, got.StorageReplicas, "a change never reached the reader")
}

func TestAFailedAskKeepsTheLastAnswer(t *testing.T) {
	src := &saying{region: Region{StorageReplicas: 3}}
	now := time.Unix(1_700_000_000, 0)
	c := reading(t, src, &now)

	_, err := c.Settings(t.Context())
	require.NoError(t, err)

	src.set(Region{}, errors.New("cm went away"))
	now = now.Add(2 * Recheck)
	got, err := c.Settings(t.Context())
	require.NoError(t, err, "an outage made a reader forget what it had heard")
	require.Equal(t, 3, got.StorageReplicas)
}

func TestCmOnTheSameNodeIsReadWithNoClientDialled(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := reading(t, &saying{region: Region{StorageReplicas: 4}}, &now)

	got, err := c.Settings(t.Context())
	require.NoError(t, err)
	require.Equal(t, 4, got.StorageReplicas)
	_, held := c.reach.At()
	require.False(t, held, "a client was dialled for the cm on this node")
}
