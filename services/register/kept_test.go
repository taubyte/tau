package register

import (
	"context"
	"errors"
	"testing"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// fixed is a reader's register nodes, named.
type fixed []libp2ppeer.ID

func (f fixed) registers() []libp2ppeer.ID { return f }

// connected starts a register on each of n nodes, links them and a reader, and
// connects every pair.
func connected(t *testing.T, n int) (registers []*node, services []Service, reader *node) {
	t.Helper()
	for range n {
		at := mockNode(t)
		registers = append(registers, at)
	}
	reader = mockNode(t)
	require.NoError(t, peer.LinkAllPeers())
	all := append(append([]*node{}, registers...), reader)
	for i, a := range all {
		for _, b := range all[i+1:] {
			require.NoError(t, a.Host().Connect(t.Context(), libp2ppeer.AddrInfo{ID: b.ID(), Addrs: b.Host().Addrs()}))
		}
	}
	for _, at := range registers {
		s := New()
		t.Cleanup(func() { s.Close() })
		require.NoError(t, s.Start(t.Context(), at))
		services = append(services, s)
	}
	return registers, services, reader
}

func ids(nodes []*node) fixed {
	var out fixed
	for _, n := range nodes {
		out = append(out, n.ID())
	}
	return out
}

// live waits for the reader to hold n sessions.
func live(t *testing.T, v *Views, n int) {
	t.Helper()
	v.w.pick()
	require.Eventually(t, func() bool {
		v.w.mu.Lock()
		defer v.w.mu.Unlock()
		return len(v.w.live) == n
	}, 30*time.Second, 20*time.Millisecond, "the reader never held %d sessions", n)
}

var watchedOnly = keptLimits{max: 1 << 10, watched: time.Hour, unwatched: time.Hour}

func claim(t *testing.T, s Service, project string, revision uint64, hosts ...string) {
	t.Helper()
	_, err := s.Local().Publish(t.Context(), pkgregister.Publication{
		Project: project, Environment: "prod", Revision: revision, Compiled: []byte(project), Hosts: hosts,
	})
	require.NoError(t, err)
}

func TestANameNobodyClaimedIsKeptUntilItIsClaimed(t *testing.T) {
	registers, services, reader := connected(t, 1)
	v := viewsOn(reader, ids(registers), defaultWatcherLimits, watchedOnly)
	t.Cleanup(func() { v.Close() })
	live(t, v, 1)

	_, _, err := v.ResolveHost(t.Context(), "new.example.com")
	require.ErrorIs(t, err, pkgregister.ErrNoHost)
	held, ok := v.hosts.answers.Load(hostRead{fqdn: "new.example.com"})
	require.True(t, ok, "the absence is kept")
	require.NotNil(t, held.(*answer[hostRead, hostAnswer]).s, "and watched")

	// The budget is an hour: only the register's word can make the claim visible.
	claim(t, services[0], "shop", 1, "new.example.com")
	require.Eventually(t, func() bool {
		project, _, err := v.ResolveHost(t.Context(), "New.Example.com.")
		return err == nil && project == "shop"
	}, 10*time.Second, 10*time.Millisecond)
}

func TestTwoRegisterNodesKeepAReaderWhenOneGoes(t *testing.T) {
	registers, services, reader := connected(t, 2)
	v := viewsOn(reader, ids(registers), defaultWatcherLimits, watchedOnly)
	t.Cleanup(func() { v.Close() })
	live(t, v, 2)

	names := []string{"a.example.com", "b.example.com", "c.example.com", "d.example.com"}
	for _, name := range names {
		_, _, err := v.ResolveHost(t.Context(), name)
		require.ErrorIs(t, err, pkgregister.ErrNoHost)
	}
	on := map[libp2ppeer.ID]int{}
	for _, name := range names {
		held, _ := v.hosts.answers.Load(hostRead{fqdn: name})
		on[held.(*answer[hostRead, hostAnswer]).s.peer]++
	}
	require.Len(t, on, 2, "the reads are spread over both nodes")

	// Claimed on one node: entries bound on the other are told once it merges.
	claim(t, services[0], "shop", 1, names...)
	require.Eventually(t, func() bool {
		for _, name := range names {
			if project, _, err := v.ResolveHost(t.Context(), name); err != nil || project != "shop" {
				return false
			}
		}
		return true
	}, 30*time.Second, 20*time.Millisecond, "every name was claimed, whichever node watched it")

	// One node goes: its answers go with it, and the reader reads on through the other.
	require.NoError(t, services[0].Close())
	live(t, v, 1)
	for _, name := range names {
		held, ok := v.hosts.answers.Load(hostRead{fqdn: name})
		if ok {
			require.NotEqual(t, registers[0].ID(), held.(*answer[hostRead, hostAnswer]).s.peer, "nothing is kept through a node that went")
		}
		project, _, err := v.ResolveHost(t.Context(), name)
		require.NoError(t, err)
		require.Equal(t, "shop", project)
	}
}

func TestAViewAndTheFleetAreKeptUntilTheyMove(t *testing.T) {
	registers, services, reader := connected(t, 1)
	reader.region = "region-1"
	v := viewsOn(reader, ids(registers), defaultWatcherLimits, watchedOnly)
	t.Cleanup(func() { v.Close() })
	live(t, v, 1)

	publish := func(revision uint64, body string) {
		_, err := services[0].Local().Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: revision,
			Compiled: []byte(body), Views: []pkgregister.View{{Destination: "region-1", Compiled: []byte(body + " region-1")}}})
		require.NoError(t, err)
	}
	publish(1, "v1")
	p, err := v.Resolve(t.Context(), "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, uint64(1), p.Revision)
	publish(2, "v2")
	require.Eventually(t, func() bool {
		p, err := v.Resolve(t.Context(), "shop", "prod")
		return err == nil && p.Revision == 2
	}, 10*time.Second, 10*time.Millisecond)

	_, err = v.ResolveFleet(t.Context())
	require.ErrorIs(t, err, pkgregister.ErrNotPublished)
	_, err = services[0].Local().PublishFleet(t.Context(), 1, []byte("fleet"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		f, err := v.ResolveFleet(t.Context())
		return err == nil && f.Revision == 1
	}, 10*time.Second, 10*time.Millisecond)
}

func TestAnEvictedAnswerIsReleasedOnTheRegister(t *testing.T) {
	registers, services, reader := connected(t, 1)
	limits := watchedOnly
	limits.max = 1
	v := viewsOn(reader, ids(registers), defaultWatcherLimits, limits)
	t.Cleanup(func() { v.Close() })
	live(t, v, 1)

	for _, name := range []string{"a.example.com", "b.example.com"} {
		_, _, err := v.ResolveHost(t.Context(), name)
		require.ErrorIs(t, err, pkgregister.ErrNoHost)
	}
	require.Equal(t, 1, v.hosts.len(), "past its bound the oldest is let go")
	srv := services[0].(*server)
	require.Eventually(t, func() bool {
		srv.watches.mu.Lock()
		defer srv.watches.mu.Unlock()
		total := 0
		for _, s := range srv.watches.sessions {
			total += len(s.entries)
		}
		return total == 1
	}, 10*time.Second, 10*time.Millisecond, "and released where it was bound")
}

func TestWithNoSessionAnAnswerLastsTheDeployBudget(t *testing.T) {
	registers, services, reader := connected(t, 1)
	limits := defaultWatcherLimits
	limits.sessions = 0
	v := viewsOn(reader, ids(registers), limits, defaultKeptLimits)
	t.Cleanup(func() { v.Close() })
	at := time.Unix(1_700_000_000, 0)
	v.hosts = newKept[hostRead, hostAnswer](v.w, keptLimits{max: 8, watched: time.Hour, unwatched: time.Minute}, func() time.Time { return at })

	_, _, err := v.ResolveHost(t.Context(), "shop.example.com")
	require.ErrorIs(t, err, pkgregister.ErrNoHost)
	held, ok := v.hosts.answers.Load(hostRead{fqdn: "shop.example.com"})
	require.True(t, ok)
	require.Nil(t, held.(*answer[hostRead, hostAnswer]).s, "kept unwatched")

	claim(t, services[0], "shop", 1, "shop.example.com")
	_, _, err = v.ResolveHost(t.Context(), "shop.example.com")
	require.ErrorIs(t, err, pkgregister.ErrNoHost, "inside the budget it is what was kept")

	at = at.Add(2 * time.Minute)
	project, _, err := v.ResolveHost(t.Context(), "shop.example.com")
	require.NoError(t, err, "past it the register is asked again")
	require.Equal(t, "shop", project)
}

func TestAReadThatCouldNotLookKeepsNothing(t *testing.T) {
	registers, services, reader := connected(t, 1)
	v := viewsOn(reader, ids(registers), defaultWatcherLimits, watchedOnly)
	t.Cleanup(func() { v.Close() })
	live(t, v, 1)
	require.NoError(t, services[0].Close())

	_, _, err := v.ResolveHost(t.Context(), "shop.example.com")
	require.Error(t, err)
	require.False(t, errors.Is(err, pkgregister.ErrNoHost), "an unreachable register is no absence")
	require.Zero(t, v.hosts.len())
}

// silent is a session's stream that says nothing until it is cancelled.
type silent struct{ ctx context.Context }

func (s silent) Receive() bool             { <-s.ctx.Done(); return false }
func (silent) Msg() *registerv2.WatchEvent { return nil }
func (s silent) Err() error                { return s.ctx.Err() }

func TestASessionThatStopsBeatingIsEnded(t *testing.T) {
	registers, _, reader := connected(t, 1)
	c, err := NewClient(reader.Host(), registers[0].ID())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	told := make(chan struct{})
	s := &watchSession{peer: registers[0].ID(), client: c, beat: 10 * time.Millisecond, stream: silent{ctx}, cancel: cancel,
		entries: map[uint64]keeper{1: tellOnce(told)}}

	done := make(chan struct{})
	go func() { s.follow(2); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a silent session was never ended")
	}
	require.True(t, s.dead.Load())
	select {
	case <-told:
	default:
		t.Fatal("what it kept was not let go")
	}
	require.False(t, s.hold(2, tellOnce(make(chan struct{}))), "a dead session holds nothing new")
}

// tellOnce is a kept answer that closes its channel when told.
type tellOnce chan struct{}

func (t tellOnce) told() { close(t) }

func TestAReaderWithNoRegisterAnywhereIsRefusedByName(t *testing.T) {
	v := viewsOn(mockNode(t), fixed{}, defaultWatcherLimits, defaultKeptLimits)
	t.Cleanup(func() { v.Close() })
	_, _, err := v.ResolveHost(t.Context(), "shop.example.com")
	require.ErrorIs(t, err, service.ErrNoneRuns)
}
