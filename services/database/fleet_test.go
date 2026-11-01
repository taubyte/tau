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

func document(t *testing.T, replication int) []byte {
	t.Helper()
	b, err := fleet.Encode(fleet.Fleet{Replication: replication})
	require.NoError(t, err)
	return b
}

func TestTheFloorComesFromTheFleetDocument(t *testing.T) {
	at := &fleetFloor{from: &fleetSaying{doc: document(t, 3), revision: 7}}

	floor, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, 3, floor)
	require.Equal(t, uint64(7), at.revisionSeen(), "a node behind is behind by a revision it can name")
}

func TestAnUnpublishedFleetIsTheDefault(t *testing.T) {
	at := &fleetFloor{from: &fleetSaying{}}

	floor, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, DefaultReplication, floor)
}

func TestUnreachableMeansThisNodeMayNotRelease(t *testing.T) {
	at := &fleetFloor{from: &fleetSaying{err: errors.New("no route to the register")}}

	_, known := at.read(t.Context())
	require.False(t, known, "a node released data on a floor it had never read")
}

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

func TestAMalformedFleetDocumentDoesNotBecomeTheDefault(t *testing.T) {
	at := &fleetFloor{from: &fleetSaying{doc: []byte("{not json"), revision: 1}}

	_, known := at.read(t.Context())
	require.False(t, known)
}

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

func TestAServiceWithNoFleetSourceKnowsTheDefault(t *testing.T) {
	var at *fleetFloor
	floor, known := at.read(t.Context())
	require.True(t, known)
	require.Equal(t, DefaultReplication, floor)
}
