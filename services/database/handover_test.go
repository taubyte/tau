package database

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/placement"
)

// pair is two real services that can reach each other, both holding the same
// database — which is what the release gate needs, since it is not a decision a
// node makes alone.
//
// THE FLEET IS BUILT BEFORE THE SERVICES, and nothing about a service is
// mutated after Start: the sweep is already running by then, so a test that
// assigned to a field afterwards would be racing its own subject.
type pair struct {
	at     address
	nodes  map[string]*Service
	holds  map[string]*says
	fleet  map[string][]placement.Member
	going  string // the peer placement does NOT want
	saying *says
}

func pairOf(t *testing.T, floor int) *pair {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	hosts := make([]host.Host, 2)
	for i := range hosts {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		hosts[i] = h
	}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	at := address{project: projectID, environment: "prod", id: kvID}
	fleet := map[string][]placement.Member{"eu": {
		{ID: hosts[0].ID().String(), Accepts: true},
		{ID: hosts[1].ID().String(), Accepts: true},
	}}

	// WHICHEVER PLACEMENT WANTS, THE OTHER IS THE ONE THAT LEAVES. Asserting
	// about a fixed one of the two would be asserting about a hash.
	want := placement.Desired(resourceOf(at), fleet["eu"], floor)
	require.NotEmpty(t, want)
	keeping := map[string]bool{}
	for _, id := range want {
		keeping[id] = true
	}

	out := &pair{at: at, fleet: fleet, nodes: map[string]*Service{}, holds: map[string]*says{}}
	for _, h := range hosts {
		peer := h.ID().String()
		holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}

		registry := database.NewRegistry()
		require.NoError(t, registry.Register(t.Context(), &accounting{}))

		s, err := New(h,
			WithEngines(registry),
			WithRegister(declaringFloor(t, compiled(), floor)),
			WithHoldings(holds),
			WithRegion("eu"),
			WithMembers(func() map[string][]placement.Member { return fleet }),
			WithIdle(time.Hour),
			// LONG, so the only sweep in these tests is the one the test runs.
			// A ticking one would be a second actor racing the assertion.
			WithSweep(time.Hour))
		require.NoError(t, err)
		require.NoError(t, s.Start(t.Context()))
		t.Cleanup(func() { s.Close() })

		out.nodes[peer], out.holds[peer] = s, holds
		if !keeping[peer] {
			out.going, out.saying = peer, holds
		}
	}
	require.NotEmpty(t, out.going, "placement wanted both nodes; the floor is too high for this test")
	return out
}

// write puts the same row through one node's local surface.
func (p *pair) write(t *testing.T, peer, key, value string) {
	t.Helper()
	kv, err := p.nodes[peer].Local().OpenKV(t.Context(), sessionsGrant())
	require.NoError(t, err)
	require.NoError(t, kv.Put(t.Context(), key, []byte(value)))
}

// TestANODEHANDSOVERTOONETHATHASIT, over the wire, with the gate deciding —
// which is the whole arc: placement says this node should no longer hold it, the
// node asks its replacement, the replacement proves it has the same rows and
// means to keep them, and only then is anything deleted.
func TestADatabaseIsReleasedOnlyToANodeThatHasIt(t *testing.T) {
	p := pairOf(t, 1)

	// BOTH HOLD THE SAME ROWS, which is what the digest compares.
	for peer := range p.nodes {
		p.write(t, peer, "session:1", "alice")
	}

	p.nodes[p.going].reconsider(t.Context())
	require.Len(t, p.saying.destroyed, 1, "the node that should not hold it kept it")
	require.Equal(t, kvID, p.saying.destroyed[0].Name)
}

// TestITSTAYSWHENTHEREPLACEMENTHASNOTCAUGHTUP, which is the failure the gate
// exists for: eu goes dark, an operator repins, the new holders are alive and
// received nothing, eu comes back and must not drop the only copies.
func TestADatabaseIsNotReleasedToANodeThatIsBehind(t *testing.T) {
	p := pairOf(t, 1)

	// ONLY THE DEPARTING NODE HAS THE ROWS.
	p.write(t, p.going, "session:1", "alice")

	p.nodes[p.going].reconsider(t.Context())
	require.Empty(t, p.saying.destroyed,
		"the only copy was deleted because its replacement was merely alive")
}

// TestTHESWEEPRUNSONITSOWN, or every one of these decisions is one nobody makes.
func TestTheSweepRunsOnItsInterval(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h, err := mn.GenPeer()
	require.NoError(t, err)

	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	registry := database.NewRegistry()
	require.NoError(t, registry.Register(t.Context(), &accounting{}))

	fleet := map[string][]placement.Member{"eu": {{ID: h.ID().String(), Accepts: true}}}
	s, err := New(h,
		WithEngines(registry),
		WithRegister(declaringFloor(t, compiled(), 1)),
		WithHoldings(holds),
		WithRegion("eu"),
		WithMembers(func() map[string][]placement.Member { return fleet }),
		WithIdle(time.Hour),
		WithSweep(5*time.Millisecond))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { s.Close() })

	require.Eventually(t, func() bool { return holds.reads() > 0 },
		time.Second, 5*time.Millisecond, "the sweep never asked what this node holds")
}

// bothDesired is two nodes that BOTH hold a database and both should, which is
// the shape repair runs in: a holder checking its co-holder is still there.
func bothDesired(t *testing.T, otherHolds bool) (*Service, *says) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	hosts := make([]host.Host, 2)
	for i := range hosts {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		hosts[i] = h
	}
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	fleet := map[string][]placement.Member{"eu": {
		{ID: hosts[0].ID().String(), Accepts: true},
		{ID: hosts[1].ID().String(), Accepts: true},
	}}

	build := func(h host.Host, holds *says) *Service {
		registry := database.NewRegistry()
		require.NoError(t, registry.Register(t.Context(), &accounting{}))

		// FLOOR TWO OVER TWO NODES, so both are desired and neither is leaving.
		s, err := New(h,
			WithEngines(registry),
			WithRegister(declaringFloor(t, compiled(), 2)),
			WithHoldings(holds),
			WithRegion("eu"),
			WithMembers(func() map[string][]placement.Member { return fleet }),
			WithIdle(time.Hour),
			WithSweep(time.Hour))
		require.NoError(t, err)
		require.NoError(t, s.Start(t.Context()))
		t.Cleanup(func() { s.Close() })
		return s
	}

	mine := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	build(hosts[1], &says{held: otherHolds})
	return build(hosts[0], mine), mine
}

// TestAHOLDERTHATCANSEEITSCOHOLDERREPORTSNOTHINGPENDING.
//
// This is repair's quiet case and it has to be quiet, or the number means
// "this cloud is running" rather than "something needs attention".
func TestACoHolderThatAnswersIsNotPending(t *testing.T) {
	s, holds := bothDesired(t, true)

	s.reconsider(t.Context())
	require.Zero(t, s.Pending())
	require.Empty(t, holds.destroyed, "repair deleted something")
}

// TestACOHOLDERTHATHASNOTGOTITISPENDING.
//
// "Nobody lets go until somebody else has it" covers a MOVE and says nothing
// about node LOSS: a crashed node's copies are gone, and opening on demand
// restores the floor only for databases somebody happens to ask for. This is
// what notices — from the node that has the data, which is why it needs no
// shared index of who-holds-what.
func TestACoHolderThatDoesNotHoldItIsPending(t *testing.T) {
	s, holds := bothDesired(t, false)

	s.reconsider(t.Context())
	require.Equal(t, 1, s.Pending(),
		"a database whose second copy does not exist was counted as fine")
	require.Empty(t, holds.destroyed, "repair is not allowed to delete anything")
}
