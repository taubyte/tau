package database

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/database"
	databasev1 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v1"
	"github.com/taubyte/tau/pkg/proto/gen/tau/database/v1/databasev1connect"
)

// says is a Holdings that answers whatever a test needs, and records nothing
// else: what this file is about is whether ASKING changes anything.
type says struct {
	held      bool
	destroyed []database.Spec
}

func (s *says) Held(context.Context, database.Spec) bool { return s.held }
func (s *says) OnDisk(context.Context) []database.Spec   { return nil }
func (s *says) Destroy(_ context.Context, spec database.Spec) error {
	s.destroyed = append(s.destroyed, spec)
	return nil
}

func asking(t *testing.T, holds *says) (databasev1connect.DatabaseServiceClient, *accounting) {
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

	s, err := New(server,
		WithEngines(registry),
		WithRegister(&published{revision: 1, object: compiled()}),
		WithHoldings(holds),
		WithIdle(time.Hour))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	conn, err := rpc.NewClient(caller, server.ID(), Protocol)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return databasev1connect.NewDatabaseServiceClient(conn.HTTP(), conn.URL()), engine
}

func sessionsRef() *databasev1.Ref {
	return &databasev1.Ref{Project: projectID, Environment: "prod", Id: kvID}
}

// TestASKINGDOESNOTMAKEITSO.
//
// KVDigest used to answer by opening the database, and opening subscribes — so
// a node asked "do you hold this?" BECAME a holder by being asked, and then
// answered a digest that looked converged. That is how a node placement never
// chose ends up authorising another node's drop, and it is the mechanism behind
// every interleaving where two nodes delete a database between them.
func TestAskingANodeDoesNotMakeItAHolder(t *testing.T) {
	client, engine := asking(t, &says{held: false})

	out, err := client.KVDigest(t.Context(), connect.NewRequest(sessionsRef()))
	require.NoError(t, err)
	require.False(t, out.Msg.GetHeld(), "a node that holds nothing said it does")
	require.Empty(t, out.Msg.GetDigest())

	opened, _ := engine.counts()
	require.Zero(t, opened, "asking whether a node holds a database opened it")
}

// TestHoldIsTheCheapHalf, which is what repair asks: presence, not proof.
func TestHoldAnswersWithoutADigest(t *testing.T) {
	client, engine := asking(t, &says{held: true})

	out, err := client.Hold(t.Context(), connect.NewRequest(sessionsRef()))
	require.NoError(t, err)
	require.True(t, out.Msg.GetHeld())

	opened, _ := engine.counts()
	require.Zero(t, opened, "a presence check computed a digest")
}

// TestANodeHoldingACopyDigestsIt, so the gate has something to compare.
func TestAHeldDatabaseIsDigested(t *testing.T) {
	client, _ := asking(t, &says{held: true})

	out, err := client.KVDigest(t.Context(), connect.NewRequest(sessionsRef()))
	require.NoError(t, err)
	require.True(t, out.Msg.GetHeld())
	require.NotEmpty(t, out.Msg.GetDigest(), "a held database answered no digest")

	// AND IT KEEPS IT. Serving a database is holding it, and a node holding one
	// means to keep it until its own release says otherwise — a node that
	// answered keeps=false here would make every move in the fleet impossible,
	// since the gate counts only keepers.
	require.True(t, out.Msg.GetKeeps())
}
