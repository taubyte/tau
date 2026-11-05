// The tests are in row_test rather than in row, so that everything they can
// reach is everything a caller can reach: if a row could be filled, read back
// or handed to a store from outside, it would be done here.
package row_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/services/plm/jobs/row"
)

// applied is the only way a test can see what went into an entry, which is the
// point: there is no accessor, so the rows are visible where the store sees them
// and nowhere earlier.
type applied struct {
	rows    []string
	batches int
	commits int
	// fail makes the named step refuse: "batch", "put", "delete" or "commit".
	fail string
}

var errRefused = errors.New("the store is full")

func (a *applied) Batch(context.Context) (kvdb.Batch, error) {
	a.batches++
	if a.fail == "batch" {
		return nil, errRefused
	}
	return &batch{a: a}, nil
}

type batch struct{ a *applied }

func (b *batch) Put(key string, value []byte) error {
	if b.a.fail == "put" {
		return errRefused
	}
	b.a.rows = append(b.a.rows, "set "+key+"="+string(value))
	return nil
}

func (b *batch) Delete(key string) error {
	if b.a.fail == "delete" {
		return errRefused
	}
	b.a.rows = append(b.a.rows, "delete "+key)
	return nil
}

func (b *batch) Commit() error {
	if b.a.fail == "commit" {
		return errRefused
	}
	b.a.commits++
	return nil
}

func TestARowIsAKeyAndAVerbInOneDelta(t *testing.T) {
	var e row.Entry
	e.Set("a", []byte("v"))
	e.Delete("b")
	require.Equal(t, 2, e.Len())

	var to applied
	require.NoError(t, e.Apply(t.Context(), &to))
	require.Equal(t, []string{"set a=v", "delete b"}, to.rows)
	require.Equal(t, 1, to.batches, "one entry, one delta")
	require.Equal(t, 1, to.commits)
}

func TestTheRowsComeOutInTheOrderTheyWentIn(t *testing.T) {
	e := row.Sized(4)
	for _, key := range []string{"a", "b", "c", "d"} {
		e.Set(key, nil)
	}
	var to applied
	require.NoError(t, e.Apply(t.Context(), &to))
	require.Equal(t, []string{"set a=", "set b=", "set c=", "set d="}, to.rows)
}

func TestTheOnlyEntryAcallerCanBuildForItselfHoldsNothing(t *testing.T) {
	var e row.Entry
	require.Zero(t, e.Len())

	var to applied
	require.ErrorIs(t, e.Apply(t.Context(), &to), row.ErrNoRows)
	require.Zero(t, to.batches, "an entry with no rows must not reach the store at all")
}

func TestAStoreThatRefusesAnyStepRefusesTheEntry(t *testing.T) {
	for _, step := range []string{"batch", "put", "delete", "commit"} {
		e := row.Sized(2)
		e.Set("k", []byte("v"))
		e.Delete("gone")
		to := applied{fail: step}
		require.ErrorIs(t, e.Apply(t.Context(), &to), errRefused, step)
		require.Zero(t, to.commits, "%s: nothing is committed", step)
	}

	// AND APPLYING TWICE IS TWO DELTAS, not an entry that empties itself. The
	// caller owns the value; what it cannot do is change what is in it.
	e := row.Sized(1)
	e.Set("k", []byte("v"))
	var to applied
	require.NoError(t, e.Apply(t.Context(), &to))
	require.NoError(t, e.Apply(t.Context(), &to))
	require.Equal(t, 2, to.commits)
	require.Equal(t, 1, e.Len())
}

func TestACopySharesTheRowsAndIsNotASecondEntry(t *testing.T) {
	e := row.Sized(4)
	e.Set("shared", nil)

	other := e
	other.Set("second", nil)
	e.Set("also second", nil)

	var to applied
	require.NoError(t, other.Apply(t.Context(), &to))
	require.Equal(t, []string{"set shared=", "set also second="}, to.rows,
		"one entry, whichever variable named it")
}
