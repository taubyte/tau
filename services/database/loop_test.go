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
func sweeping(t *testing.T, holds *says, at []placement.Member, floor int) *Service {
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
		WithRegister(declaringFloor(t, compiled(), floor)),
		WithHoldings(holds),
		WithMembers(func() []placement.Member { return at }),
		WithIdle(time.Hour))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func sessionsSpec() database.Spec {
	return database.Spec{Type: database.KV, Project: projectID, Environment: "prod", Name: kvID}
}

func TestASweepKeepsWhatItShould(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	fleet := members("me")

	s := sweeping(t, holds, fleet, 1)
	s.peer = "me"

	s.reconsider(t.Context())
	require.Empty(t, holds.destroyed, "a node deleted a copy it should hold")

	at := address{project: projectID, environment: "prod", id: kvID}
	require.True(t, s.own.keeps(at.key()),
		"a node holding a database did not record keeping it, so no peer could ever leave")
}

func TestASweepHoldsOnWhenItsReplacementCannotBeReached(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	// `other` is desired and is not this node — and is not dialable either.
	fleet := members("other")

	s := sweeping(t, holds, fleet, 1)
	s.peer = "me"

	s.reconsider(t.Context())
	require.Empty(t, holds.destroyed,
		"the data was deleted although its replacement never answered")

	// And it is still held, so the next sweep tries again.
	at := address{project: projectID, environment: "prod", id: kvID}
	require.True(t, s.own.keeps(at.key()))
}

func TestASweepWalksOnlyWhatThisNodeHolds(t *testing.T) {
	holds := &says{held: true}
	s := sweeping(t, holds, members("other"), 1)
	s.peer = "me"

	s.reconsider(t.Context())
	require.Empty(t, holds.destroyed)
}

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

func TestASweepCountsACoHolderItCannotConfirm(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	// Two desired at a floor of two, and the other one is not dialable.
	fleet := members("me", "other")

	s := sweeping(t, holds, fleet, 2)
	s.peer = "me"

	s.reconsider(t.Context())
	require.Empty(t, holds.destroyed, "repair deleted something")
	require.Equal(t, 1, s.Pending(),
		"a database whose second copy could not be confirmed was counted as fine")
}

func TestASweepDoesNotReportASmallFleetAsShort(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	fleet := members("me")

	s := sweeping(t, holds, fleet, 2)
	s.peer = "me"

	s.reconsider(t.Context())
	require.Zero(t, s.Pending())
}

func TestASweepReportsTheFleetRevisionItComputedWith(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	s := sweeping(t, holds, members("me"), 1)
	s.peer = "me"

	s.reconsider(t.Context())
	require.Equal(t, uint64(1), s.FleetRevision())
}

func TestASweepLeavesWhatItCannotResolve(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{{
		Type: database.KV, Project: projectID, Environment: "prod", Name: "QmNobodyDeclaresThis",
	}}}

	s := sweeping(t, holds, members("me"), 1)
	s.peer = "me"

	s.reconsider(t.Context())
	require.Empty(t, holds.destroyed, "a copy nobody could resolve was deleted")
	require.Zero(t, s.Pending(), "it was counted as needing repair instead of being left alone")
}
