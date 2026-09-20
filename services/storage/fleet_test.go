package storage

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/fleet"
	"github.com/taubyte/tau/pkg/register"
)

// fleetSaying is a cloud that answers whatever a test needs about itself.
type fleetSaying struct {
	doc      []byte
	revision uint64
	err      error
	reads    int
}

func (c *fleetSaying) ResolveFleet(context.Context) (register.Pointer, error) {
	c.reads++
	if c.err != nil {
		return register.Pointer{}, c.err
	}
	if c.doc == nil {
		return register.Pointer{}, fmt.Errorf("%w: the fleet", register.ErrNotPublished)
	}
	return register.Pointer{Object: register.IDOf(c.doc), Revision: c.revision}, nil
}

func (c *fleetSaying) Object(_ context.Context, id register.ObjectID) ([]byte, error) {
	if c.doc != nil && id == register.IDOf(c.doc) {
		return c.doc, nil
	}
	return nil, errors.New("no such object")
}

func promising(t *testing.T, replicas, barrier int) []byte {
	t.Helper()
	b, err := fleet.Encode(fleet.Fleet{Storage: fleet.Storage{Replicas: replicas, Barrier: barrier}})
	require.NoError(t, err)
	return b
}

// TestRANDKAREWHATTHECLOUDPUBLISHED, not what this node was configured with.
//
// R was per-node config, which made two nodes given different numbers compute
// different desired sets FOREVER — unlike every other placement input, which
// heals as views converge — and billed a tenant's bytes at whichever node was
// asked. K was per-node config, which let one node ack writes alone with
// nothing in the cloud able to notice.
func TestRAndKComeFromTheFleetDocument(t *testing.T) {
	at := &fleetPolicy{from: &fleetSaying{doc: promising(t, 5, 3), revision: 7}}

	replicas, barrier, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, 5, replicas)
	require.Equal(t, 3, barrier)
	require.Equal(t, uint64(7), at.revisionSeen(), "a node behind is behind by a revision it can name")
}

// TestACLOUDTHATSAIDNOTHINGISSTILLAGREEMENT: every node reads the same absence
// and reaches the same defaults, so they still place identically and still
// promise the same thing.
func TestAnUnpublishedFleetIsTheDefaults(t *testing.T) {
	at := &fleetPolicy{from: &fleetSaying{}}

	replicas, barrier, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, DefaultReplicas, replicas)
	require.Equal(t, DefaultBarrier, barrier)
}

// TestUNREACHABLEISCHANGENOTHING, AND NOT SAY NOTHING. A node that has never
// reached the register has no R to measure a delete against and no K to say
// what a write waited for — and the two consequences differ, which is what the
// two gate tests below hold.
func TestUnreachableMeansThisNodeHasLearnedNeitherNumber(t *testing.T) {
	at := &fleetPolicy{from: &fleetSaying{err: errors.New("no route to the register")}}

	_, _, known := at.read(t.Context())
	require.False(t, known, "a node computed with numbers it had never read")
	require.Zero(t, at.revisionSeen())
}

// TestONCEREADTHEYSURVIVEANOUTAGE, because neither number changed — only this
// node's ability to re-check them did.
func TestKnownValuesSurviveTheRegisterGoingAway(t *testing.T) {
	source := &fleetSaying{doc: promising(t, 5, 3), revision: 1}
	at := &fleetPolicy{from: source}

	replicas, barrier, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, 5, replicas)
	require.Equal(t, 3, barrier)

	source.err = errors.New("no route to the register")
	replicas, barrier, known = at.read(t.Context())
	require.True(t, known, "an outage made a node forget what it had read")
	require.Equal(t, 5, replicas)
	require.Equal(t, 3, barrier)
}

// TestACORRUPTDOCUMENTISNOTSILENCE. Falling back to the defaults would have
// this node place differently from one that read the document before it broke,
// which is the disagreement a published number exists to end.
func TestAMalformedFleetDocumentDoesNotBecomeTheDefaults(t *testing.T) {
	at := &fleetPolicy{from: &fleetSaying{doc: []byte("{not json"), revision: 1}}

	_, _, known := at.read(t.Context())
	require.False(t, known)
}

// TestAPAIRTHATCANNOTBECORRECTISTHESAMECASE. A barrier above the replica
// target fails every write, and it is now the CLOUD's to declare — so it
// arrives at a node already serving rather than stopping one from starting.
// The reader refuses it exactly like a document that will not parse: it keeps
// what it had, and has nothing if it had nothing.
func TestAPairThatCannotBeCorrectIsRefused(t *testing.T) {
	source := &fleetSaying{doc: promising(t, 2, 3), revision: 1}
	at := &fleetPolicy{from: source}

	_, _, known := at.read(t.Context())
	require.False(t, known)

	// And a node that HAD read a good document keeps it rather than adopting
	// the impossible one.
	source.doc, source.revision = promising(t, 4, 2), 2
	_, _, known = at.read(t.Context())
	require.True(t, known)

	source.doc, source.revision = promising(t, 2, 3), 3
	replicas, barrier, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, 4, replicas, "a node adopted a pair that would fail every write")
	require.Equal(t, 2, barrier)
	require.Equal(t, uint64(2), at.revisionSeen(), "the refused revision was recorded as computed with")
}

// TestAHALFWRITTENDOCUMENTFILLSTHEOTHERHALF. Zero is the reader's default per
// field, so a cloud that raised only R still promises the default barrier —
// and every node fills it the same way, so they still agree.
func TestAFieldLeftUnsetIsTheReadersDefault(t *testing.T) {
	at := &fleetPolicy{from: &fleetSaying{doc: promising(t, 5, 0), revision: 1}}
	replicas, barrier, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, 5, replicas)
	require.Equal(t, DefaultBarrier, barrier)

	at = &fleetPolicy{from: &fleetSaying{doc: promising(t, 0, 1), revision: 1}}
	replicas, barrier, known = at.read(t.Context())
	require.True(t, known)
	require.Equal(t, DefaultReplicas, replicas)
	require.Equal(t, 1, barrier)
}

// TestTHEOBJECTISREADONCE, since it is content-addressed: a pointer naming what
// this node already decoded has nothing new to say.
func TestAnUnchangedFleetIsNotDecodedAgain(t *testing.T) {
	source := &fleetSaying{doc: promising(t, 5, 3), revision: 1}
	at := &fleetPolicy{from: source}

	for range 5 {
		replicas, barrier, known := at.read(t.Context())
		require.True(t, known)
		require.Equal(t, 5, replicas)
		require.Equal(t, 3, barrier)
	}
	require.Equal(t, 5, source.reads, "the pointer is cheap and is read every time")
}

// TestNOFLEETSOURCEISTHEDEFAULTS, and it is agreed for the same reason an
// unpublished fleet is: every such node reaches them identically.
func TestAServiceWithNoFleetSourceKnowsTheDefaults(t *testing.T) {
	var at *fleetPolicy
	replicas, barrier, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, DefaultReplicas, replicas)
	require.Equal(t, DefaultBarrier, barrier)
}

// TestANODETHATHASNEVERREADRSTILLCOLLECTSADEADOBJECT, and this is the half of
// the rule that is easy to get wrong in the safe-looking direction.
//
// Gates 1-4 never mention R: a dead object is dead at every replication factor,
// because "nobody references these bytes" is not a statement about how many
// copies there should be. Refusing this would stop reclamation on every node
// that lost the register — a disk filling up, bought for no safety at all.
func TestANodeWithNoReplicaTargetStillCollectsADeadObject(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	grace := 24 * time.Hour
	at := &fleetPolicy{from: &fleetSaying{err: errors.New("no route to the register")}}

	require.NoError(t, openGates(now.Add(-48*time.Hour)).Open(DeadObject, now, grace, at.policy(t.Context())))
}

// TestANODETHATHASNEVERREADRMAYNOTDROPAMISPLACEDBLOCK. This is the storage
// transposition of "may acquire, may not release", and gate 5 is where it
// bites: ProvenHolders < R is a comparison, and a node with no R has no
// right-hand side. Guessing three where the cloud says five drops bytes two
// replicas short.
func TestANodeWithNoReplicaTargetRefusesAMisplacementDrop(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	grace := 24 * time.Hour
	at := &fleetPolicy{from: &fleetSaying{err: errors.New("no route to the register")}}

	g := openGates(now.Add(-48 * time.Hour))
	g.ProvenHolders = 99 // every holder in the cloud proved it, and it still does not open

	err := g.Open(MisplacementDrop, now, grace, at.policy(t.Context()))
	require.ErrorIs(t, err, ErrGateClosed)
	require.Contains(t, err.Error(), "never read the cloud's replica target")
}

// TestANODETHATHASNEVERREADKCANNOTACKNOWLEDGEAWRITE, which is where storage
// parts company with the database service: an unknown floor there costs
// nothing while a node keeps serving reads, and an unknown barrier here closes
// the write path.
//
// A put that cannot learn K cannot say how many acknowledgements it waited for,
// and the only honest answer is to fail the write — picking a number is the
// same shipped lie by a third route. The write path is the RPC round; what is
// held here is the value it gates on, because the gate is not a number but the
// absence of one.
func TestANodeWithNoBarrierCannotSayWhatAWriteWaitedFor(t *testing.T) {
	at := &fleetPolicy{from: &fleetSaying{err: errors.New("no route to the register")}}

	_, barrier, known := at.read(t.Context())
	require.False(t, known, "a write would have acknowledged on a barrier this node invented")
	require.Zero(t, barrier, "there is no number to fall back to, which is the point")
}

// TestKIsNeverInferred is owner decision O-5's default, at the place it bites,
// and the move to a published document narrows rather than abandons it.
//
// What is refused is DERIVING K FROM THE MEMBERSHIP VIEW: a K that adapted to
// what a node can currently see would ack a write during a partition that no
// second node ever saw. A published constant cannot tell a fleet of one from a
// partition of one either — a single-node cloud still WRITES 1, once for the
// cloud instead of once per machine.
func TestKIsNeverInferred(t *testing.T) {
	one := &fleetPolicy{from: &fleetSaying{doc: promising(t, 1, 1), revision: 1}}
	_, barrier, known := one.read(t.Context())
	require.True(t, known)
	require.Equal(t, 1, barrier, "a single-node cloud says so in its own document")

	// And the only way to get K=1 is to have written it: an absent document is
	// the default, never a count of anything.
	silent := &fleetPolicy{from: &fleetSaying{}}
	_, barrier, known = silent.read(t.Context())
	require.True(t, known)
	require.Equal(t, DefaultBarrier, barrier)
}

// TestNOTHINGDERIVESKFROMAMEMBERCOUNT is O-5 held structurally rather than by a
// comment, the way TestRaftAppearsNowhere is: the files that name a barrier at
// all cannot see a member set, so there is nothing to derive one from.
//
// It is a census over the package's own files rather than a list to keep in
// step, so a new file that started computing a barrier fails it by existing.
func TestNothingDerivesKFromAMemberCount(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	named := map[string][]string{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		require.NoError(t, err)

		mentions := false
		ast.Inspect(file, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && strings.Contains(strings.ToLower(id.Name), "barrier") {
				mentions = true
			}
			return true
		})
		if !mentions {
			continue
		}
		for _, imp := range file.Imports {
			named[name] = append(named[name], strings.Trim(imp.Path.Value, `"`))
		}
		named[name] = append(named[name], "") // a file with no imports still counts as read
	}

	require.NotEmpty(t, named, "no file names a barrier, so this census passes for free")
	for name, imports := range named {
		for _, path := range imports {
			require.NotContains(t, path, "pkg/membership",
				"%s names a barrier and can see a member set", name)
			require.NotContains(t, path, "pkg/placement",
				"%s names a barrier and can see a member set", name)
		}
	}
}
