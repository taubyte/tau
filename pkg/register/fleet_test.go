package register

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAFleetDocumentIsPublishedAndResolved(t *testing.T) {
	r, err := New(WithStore(NewMemStore()))
	require.NoError(t, err)
	ctx := t.Context()

	// Not published is its own answer, never an empty document.
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

func TestAnEmptyFleetDocumentIsRefused(t *testing.T) {
	r, err := New(WithStore(NewMemStore()))
	require.NoError(t, err)

	_, err = r.PublishFleet(t.Context(), 1, nil)
	require.True(t, errors.Is(err, ErrEmptyObject))
}
