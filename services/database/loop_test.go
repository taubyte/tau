package database

import (
	"context"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/placement"
)

// sweeping builds a service that holds one database and can see a fleet.
func sweeping(t *testing.T, holds *says, at map[string][]placement.Member, region string) *Service {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	server, err := mn.GenPeer()
	require.NoError(t, err)

	engine := &accounting{}
	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))

	s, err := New(server,
		WithEngines(registry),
		WithRegister(declaringFloor(t, compiled(), 1)),
		WithHoldings(holds),
		WithMembers(func() map[string][]placement.Member { return at }),
		WithRegion(region),
		WithIdle(time.Hour))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func sessionsSpec() database.Spec {
	return database.Spec{Type: database.KV, Project: projectID, Environment: "prod", Name: kvID}
}

// TestASWEEPKEEPSWHATITSHOULD, and records that it does — which is what makes
// its answer to a peer asking mean anything.
func TestASweepKeepsWhatItShould(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	fleet := map[string][]placement.Member{"eu": members("me")}

	s := sweeping(t, holds, fleet, "eu")
	s.peer = "me"

	s.reconsider(t.Context())
	require.Empty(t, holds.destroyed, "a node deleted a copy it should hold")

	at := address{project: projectID, environment: "prod", id: kvID}
	require.True(t, s.own.keeps(at.key()),
		"a node holding a database did not record keeping it, so no peer could ever leave")
}

// TestASWEEPWILLNOTLETGOWITHOUTANANSWER, which is the whole gate: the
// replacement here cannot be reached at all.
func TestASweepHoldsOnWhenItsReplacementCannotBeReached(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	// `other` is desired and is not this node — and is not dialable either.
	fleet := map[string][]placement.Member{"eu": members("other")}

	s := sweeping(t, holds, fleet, "eu")
	s.peer = "me"

	s.reconsider(t.Context())
	require.Empty(t, holds.destroyed,
		"the data was deleted although its replacement never answered")

	// AND IT IS STILL HELD, so the next sweep tries again rather than this one
	// having quietly given the copy up.
	at := address{project: projectID, environment: "prod", id: kvID}
	require.True(t, s.own.keeps(at.key()))
}

// TestASWEEPDOESNOTWALKWHATITDOESNOTHOLD. The sweep is O(held), not O(fleet),
// which is what lets it need no shared index of who-holds-what.
func TestASweepWalksOnlyWhatThisNodeHolds(t *testing.T) {
	holds := &says{held: true}
	s := sweeping(t, holds, map[string][]placement.Member{"eu": members("other")}, "eu")
	s.peer = "me"

	s.reconsider(t.Context())
	require.Empty(t, holds.destroyed)
}

// TestANODETHATCANNOTSEEAFLEETMOVESNOTHING, which is the honest answer for a
// node that cannot tell who else is there.
func TestASweepWithoutAFleetDoesNothing(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	server, err := mn.GenPeer()
	require.NoError(t, err)

	engine := &accounting{}
	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), engine))

	s, err := New(server,
		WithEngines(registry),
		WithRegister(&published{revision: 1, object: compiled()}),
		WithHoldings(holds),
		WithIdle(time.Hour))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	// run returns at once rather than ticking against a fleet it cannot see.
	done := make(chan struct{})
	go func() { s.run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a node with no fleet started sweeping anyway")
	}
	require.Empty(t, holds.destroyed)
}
