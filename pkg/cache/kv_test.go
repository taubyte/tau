package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
)

func TestAKvdbIsAStore(t *testing.T) {
	ctx := t.Context()
	db, err := kvdbmock.New().New(nil, "cache", 0)
	require.NoError(t, err)
	defer db.Close()
	st := Over(db)

	_, found, err := st.Get(ctx, "/a")
	require.NoError(t, err)
	require.False(t, found, "not there is not an error")

	require.NoError(t, st.Put(ctx, "/a", []byte("1")))
	v, found, err := st.Get(ctx, "/a")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("1"), v)

	keys, err := st.List(ctx, "/")
	require.NoError(t, err)
	require.Contains(t, keys, "/a")

	require.NoError(t, st.Delete(ctx, "/a"))
	require.NoError(t, st.Delete(ctx, "/a"), "deleting what is gone is done")
}

// broken is a kvdb that cannot look.
type broken struct{ kvdb.Instance }

func (broken) Get(context.Context, string) ([]byte, error) { return nil, errors.New("disk gone") }
func (broken) Delete(context.Context, string) error        { return errors.New("disk gone") }

func TestAKvdbThatCannotLookSaysSo(t *testing.T) {
	st := Over(broken{})
	_, _, err := st.Get(t.Context(), "/a")
	require.ErrorContains(t, err, "disk gone", "and is not a row that is not there")
	require.Error(t, st.Delete(t.Context(), "/a"))
}
