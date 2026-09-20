package register

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheFleetIsPUBLISHEDLIKEAPROJECT, because the reason is the same one: a
// value every node must compute with identically needs a revision, so that a
// node which is behind is behind by something it can see and name.
func TestAFleetDocumentIsPublishedAndResolved(t *testing.T) {
	r, err := New(WithStore(NewMemStore()))
	require.NoError(t, err)
	ctx := t.Context()

	// NOT PUBLISHED IS ITS OWN ANSWER, and never an empty document: a reader
	// has to tell "this cloud said nothing, use the default" from "the store
	// could not be reached, change nothing".
	_, err = r.ResolveFleet(ctx)
	require.ErrorIs(t, err, ErrNotPublished)

	id, err := r.PublishFleet(ctx, 1, []byte(`{"replication":3}`))
	require.NoError(t, err)

	at, err := r.ResolveFleet(ctx)
	require.NoError(t, err)
	require.Equal(t, id, at.Object)
	require.Equal(t, uint64(1), at.Revision)

	raw, err := r.Object(ctx, at.Object)
	require.NoError(t, err)
	require.JSONEq(t, `{"replication":3}`, string(raw))
}

// TestAFLOORDOESNOTGOBACKWARDS. One that did on one node and not another would
// be two nodes computing different placements with no way to tell which was
// stale.
func TestAFleetRevisionOnlyMovesForward(t *testing.T) {
	r, err := New(WithStore(NewMemStore()))
	require.NoError(t, err)
	ctx := t.Context()

	_, err = r.PublishFleet(ctx, 4, []byte(`{"replication":3}`))
	require.NoError(t, err)

	_, err = r.PublishFleet(ctx, 4, []byte(`{"replication":2}`))
	require.ErrorIs(t, err, ErrStaleRevision)
	_, err = r.PublishFleet(ctx, 3, []byte(`{"replication":2}`))
	require.ErrorIs(t, err, ErrStaleRevision)

	_, err = r.PublishFleet(ctx, 5, []byte(`{"replication":2}`))
	require.NoError(t, err)

	at, err := r.ResolveFleet(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(5), at.Revision)
}

// TestAnEmptyFleetDocumentIsRefused, the same as an empty project: a pointer to
// nothing is not a deploy.
func TestAnEmptyFleetDocumentIsRefused(t *testing.T) {
	r, err := New(WithStore(NewMemStore()))
	require.NoError(t, err)

	_, err = r.PublishFleet(t.Context(), 1, nil)
	require.True(t, errors.Is(err, ErrEmptyObject))
}
