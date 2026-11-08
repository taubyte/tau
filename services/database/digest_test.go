package database

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/database"
	databasev2 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v2/databasev2connect"
	"github.com/taubyte/tau/services/cm/settings"
)

// says is a Holdings that answers whatever a test needs, and records nothing
// else: what this file is about is whether ASKING changes anything.
type says struct {
	mu        sync.Mutex
	held      bool
	onDisk    []database.Spec
	destroyed []database.Spec
	asked     int

	// onHeld runs when this node is asked whether it holds a database, which is
	// the one moment a test can act from INSIDE another node's release window:
	// the asker has taken its digest and has deleted nothing yet.
	onHeld func()
}

// whenHeld arranges for f to run the next time somebody asks what this node
// holds.
func (s *says) whenHeld(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onHeld = f
}

// reads is how many times a sweep has asked what this node holds.
func (s *says) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asked
}

func (s *says) Held(context.Context, database.Spec) bool {
	s.mu.Lock()
	held, hook := s.held, s.onHeld
	s.mu.Unlock()

	// OUTSIDE THE LOCK, because the hook writes through a service that answers
	// on this same fake.
	if hook != nil {
		hook()
	}
	return held
}
func (s *says) OnDisk(context.Context) []database.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked++
	return s.onDisk
}

// Destroy behaves like the real one: released means no longer held.
func (s *says) Destroy(_ context.Context, spec database.Spec) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.destroyed = append(s.destroyed, spec)
	s.held = false
	s.onDisk = nil
	return nil
}

func asking(t *testing.T, holds *says) (databasev2connect.DatabaseServiceClient, *accounting) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	var server, caller host.Host
	var err error
	server, err = mn.GenPeer()
	require.NoError(t, err)
	caller, err = mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	engine := &accounting{}
	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))
	engine.mu.Lock()
	engine.opened, engine.live = 0, 0 // registration's probe is not a caller's
	engine.mu.Unlock()

	s, err := build(server,
		withEngines(registry),
		withRegister(&published{revision: 1, object: compiled()}),
		withHoldings(holds),
		withIdle(time.Hour))
	require.NoError(t, err)
	s.region = floorOf(settings.Defaults().Replication)
	require.NoError(t, s.serve(t.Context()))
	t.Cleanup(func() { s.Close() })

	conn, err := rpc.NewClient(caller, server.ID(), Protocol)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return databasev2connect.NewDatabaseServiceClient(conn.HTTP(), conn.URL()), engine
}

func sessionsRef() *databasev2.Ref {
	return &databasev2.Ref{Project: projectID, Environment: "prod", Id: kvID}
}

func TestAskingANodeDoesNotMakeItAHolder(t *testing.T) {
	client, engine := asking(t, &says{held: false})

	out, err := client.KVDigest(t.Context(), connect.NewRequest(sessionsRef()))
	require.NoError(t, err)
	require.False(t, out.Msg.GetHeld(), "a node that holds nothing said it does")
	require.Empty(t, out.Msg.GetDigest())

	opened, _ := engine.counts()
	require.Zero(t, opened, "asking whether a node holds a database opened it")
}

func TestHoldAnswersWithoutADigest(t *testing.T) {
	client, engine := asking(t, &says{held: true})

	out, err := client.Hold(t.Context(), connect.NewRequest(sessionsRef()))
	require.NoError(t, err)
	require.True(t, out.Msg.GetHeld())

	opened, _ := engine.counts()
	require.Zero(t, opened, "a presence check computed a digest")
}

func TestAHeldDatabaseIsDigested(t *testing.T) {
	client, _ := asking(t, &says{held: true})

	out, err := client.KVDigest(t.Context(), connect.NewRequest(sessionsRef()))
	require.NoError(t, err)
	require.True(t, out.Msg.GetHeld())
	require.NotEmpty(t, out.Msg.GetDigest(), "a held database answered no digest")

	// Serving a database is holding it and keeping it; the gate counts only keepers.
	require.True(t, out.Msg.GetKeeps())
}
