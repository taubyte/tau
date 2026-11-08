package settings_test

import (
	"context"
	"errors"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/discovery"
	"github.com/taubyte/tau/p2p/peer"
	cmmock "github.com/taubyte/tau/services/cm/mock"
	"github.com/taubyte/tau/services/cm/settings"
)

// node is an instance serving cm's protocol with the mock a test gives it, and
// finding the peers its finding names.
type node struct {
	peer.Node
	*service.Served
	finding finding
}

func newNode(t *testing.T, cm ...*cmmock.CM) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p}
	n.Served = service.NewServed(p.ID(), &n.finding)
	for _, c := range cm {
		n.Serve(settings.Protocol, c)
	}
	return n
}

func (n *node) Peer() peer.Node          { return n.Node }
func (*node) Running() []service.Running { return nil }
func (*node) Config(any) error           { return nil }
func (*node) Root() string               { return "" }
func (*node) Cloud() string              { return "settings-test" }
func (*node) Region() string             { return "" }
func (*node) Now() time.Time             { return time.Now() }

var _ service.Host = (*node)(nil)

// finding is discovery naming the peers it is given.
type finding []libp2ppeer.ID

func (f *finding) Find(protocol.ID) discovery.Resolver { return discovery.Static(*f...) }

func region(r, k int) settings.Region {
	s := settings.Defaults()
	s.StorageReplicas, s.StorageBarrier = r, k
	return s
}

func TestTheDefaultsAreWhatARegionNobodySetComputesWith(t *testing.T) {
	d := settings.Defaults()
	require.Equal(t, 2, d.Replication, "one copy of a database dies with its node")
	require.Equal(t, 3, d.StorageReplicas)
	require.Equal(t, 2, d.StorageBarrier)
	require.LessOrEqual(t, d.StorageBarrier, d.StorageReplicas)
	require.Zero(t, d.Retention, "the database's own T")
}

func TestCmOnTheSameNodeIsReadWithNoClient(t *testing.T) {
	cm := cmmock.New(region(5, 3))
	c := settings.On(newNode(t, cm))
	t.Cleanup(func() { c.Close() })

	got, err := c.Settings(t.Context())
	require.NoError(t, err)
	require.Equal(t, region(5, 3), got)
}

func TestCmOnAnotherNodeIsReadOverItsProtocol(t *testing.T) {
	ctx := t.Context()
	cm := cmmock.New(region(4, 2))
	far := newNode(t, cm)
	require.NoError(t, cm.Start(ctx, far))
	t.Cleanup(func() { cm.Close() })

	near := newNode(t)
	near.finding = finding{far.ID()}
	require.NoError(t, peer.LinkAllPeers())
	require.NoError(t, near.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: far.ID(), Addrs: far.Host().Addrs()}))

	c := settings.On(near)
	t.Cleanup(func() { c.Close() })
	got, err := c.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, region(4, 2), got)
}

func TestTheRetentionCrossesTheWireInWholeSeconds(t *testing.T) {
	ctx := t.Context()
	set := settings.Defaults()
	set.Retention = 72 * time.Hour
	cm := cmmock.New(set)
	far := newNode(t, cm)
	require.NoError(t, cm.Start(ctx, far))
	t.Cleanup(func() { cm.Close() })

	near := newNode(t)
	near.finding = finding{far.ID()}
	require.NoError(t, peer.LinkAllPeers())
	require.NoError(t, near.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: far.ID(), Addrs: far.Host().Addrs()}))

	c := settings.On(near)
	t.Cleanup(func() { c.Close() })
	got, err := c.Retention(ctx)
	require.NoError(t, err)
	require.Equal(t, 72*time.Hour, got)
}

func TestNoCmInReachIsAnErrorAndNeverTheDefaults(t *testing.T) {
	near := newNode(t)
	near.finding = finding{}
	c := settings.On(near)
	t.Cleanup(func() { c.Close() })

	_, err := c.Settings(t.Context())
	require.Error(t, err, "a reader that never heard cm computed with numbers it guessed")
}

func TestWaitReturnsOnceCmAnswers(t *testing.T) {
	cm := cmmock.New(region(3, 2))
	cm.Fail(errors.New("not up yet"))
	c := settings.On(newNode(t, cm))
	t.Cleanup(func() { c.Close() })

	go func() {
		time.Sleep(500 * time.Millisecond)
		cm.Fail(nil)
	}()
	got, err := c.Wait(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, got.StorageReplicas)
}

func TestWaitGivesUpWithItsContext(t *testing.T) {
	cm := cmmock.New(region(3, 2))
	cm.Fail(errors.New("never up"))
	c := settings.On(newNode(t, cm))
	t.Cleanup(func() { c.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err := c.Wait(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestACmThatCannotAnswerIsUnavailableOnTheWire(t *testing.T) {
	ctx := t.Context()
	cm := cmmock.New(region(3, 2))
	cm.Fail(errors.New("its store is gone"))
	far := newNode(t, cm)
	require.NoError(t, cm.Start(ctx, far))
	t.Cleanup(func() { cm.Close() })

	near := newNode(t)
	near.finding = finding{far.ID()}
	require.NoError(t, peer.LinkAllPeers())
	require.NoError(t, near.Host().Connect(ctx, libp2ppeer.AddrInfo{ID: far.ID(), Addrs: far.Host().Addrs()}))

	c := settings.On(near)
	t.Cleanup(func() { c.Close() })
	_, err := c.Settings(ctx)
	require.ErrorContains(t, err, "its store is gone")
}
