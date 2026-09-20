package database

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/fleet"
	"github.com/taubyte/tau/pkg/register"
)

// declared is a cloud that answers whatever a test needs about itself.
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

func document(t *testing.T, replication int) []byte {
	t.Helper()
	b, err := fleet.Encode(fleet.Fleet{Replication: replication})
	require.NoError(t, err)
	return b
}

// TestTheFloorIsWHATTHECLOUDPUBLISHED, not what this node was configured with.
//
// It was per-node config, which made two nodes given different numbers disagree
// about who should hold a database FOREVER — unlike every other placement input,
// which heals as views converge — and made what an operator is billed for depend
// on which node was asked.
func TestTheFloorComesFromTheFleetDocument(t *testing.T) {
	at := &fleetFloor{from: &fleetSaying{doc: document(t, 3), revision: 7}}

	floor, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, 3, floor)
	require.Equal(t, uint64(7), at.revisionSeen(), "a node behind is behind by a revision it can name")
}

// TestACLOUDTHATSAIDNOTHINGISSTILLAGREEMENT: every node reads the same absence
// and reaches the same default, so they still compute the same placement.
func TestAnUnpublishedFleetIsTheDefault(t *testing.T) {
	at := &fleetFloor{from: &fleetSaying{}}

	floor, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, DefaultReplication, floor)
}

// TestANODETHATHASNEVERREADTHEFLOORRELEASESNOTHING.
//
// The whole of release is a comparison against the floor. A node that has never
// learned it would be guessing — and guessing is how a node deletes the second
// of two copies believing the floor was one. Unreachable is not "say nothing";
// it is "change nothing".
func TestUnreachableMeansThisNodeMayNotRelease(t *testing.T) {
	at := &fleetFloor{from: &fleetSaying{err: errors.New("no route to the register")}}

	_, known := at.read(t.Context())
	require.False(t, known, "a node released data on a floor it had never read")
}

// TestONCEREADITSURVIVESANOUTAGE, because the value did not change — only this
// node's ability to re-check it did.
func TestAKnownFloorSurvivesTheRegisterGoingAway(t *testing.T) {
	source := &fleetSaying{doc: document(t, 3), revision: 1}
	at := &fleetFloor{from: source}

	floor, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, 3, floor)

	source.err = errors.New("no route to the register")
	floor, known = at.read(t.Context())
	require.True(t, known, "an outage made a node forget a floor it had read")
	require.Equal(t, 3, floor)
}

// TestACORRUPTDOCUMENTISNOTSILENCE. Falling back to the default would have this
// node place differently from one that read the document before it broke, which
// is the disagreement the published floor exists to end.
func TestAMalformedFleetDocumentDoesNotBecomeTheDefault(t *testing.T) {
	at := &fleetFloor{from: &fleetSaying{doc: []byte("{not json"), revision: 1}}

	_, known := at.read(t.Context())
	require.False(t, known)
}

// TestTHEOBJECTISREADONCE, since it is content-addressed: a pointer naming what
// this node already decoded has nothing new to say.
func TestAnUnchangedFleetIsNotDecodedAgain(t *testing.T) {
	source := &fleetSaying{doc: document(t, 3), revision: 1}
	at := &fleetFloor{from: source}

	for range 5 {
		floor, known := at.read(t.Context())
		require.True(t, known)
		require.Equal(t, 3, floor)
	}
	require.Equal(t, 5, source.reads, "the pointer is cheap and is read every time")
}

// TestNOFLEETSOURCEISTHEDEFAULT, and it is agreed for the same reason an
// unpublished one is: every such node reaches it identically.
func TestAServiceWithNoFleetSourceKnowsTheDefault(t *testing.T) {
	var at *fleetFloor
	floor, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, DefaultReplication, floor)
}
