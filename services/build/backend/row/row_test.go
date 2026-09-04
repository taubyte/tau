// The tests are in row_test rather than in row, so that everything they can
// reach is everything a caller can reach: if a row could be filled, read back
// or handed to a store from outside, it would be done here.
package row_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/build/backend/row"
)

// applied is the only way a test can see what went into an entry, which is the
// point: there is no accessor, so the batch is visible where raft would see it
// and nowhere earlier.
type applied struct {
	ops     []raft.BatchOp
	timeout time.Duration
	calls   int
	err     error
}

func (a *applied) Batch(ops []raft.BatchOp, timeout time.Duration) error {
	a.calls++
	a.ops, a.timeout = ops, timeout
	return a.err
}

// The two rows there are, in the order they went in — pkg/raft applies a batch
// in order, and an entry writes a record and the rows that index it together.
func TestARowIsAKeyAndAVerb(t *testing.T) {
	var e row.Entry
	e.Set("a", []byte("v"))
	e.Delete("b")
	require.Equal(t, 2, e.Len())

	var to applied
	require.NoError(t, e.Apply(&to, time.Second))
	require.Equal(t, []raft.BatchOp{
		{Set: &raft.SetCommand{Key: "a", Value: []byte("v")}},
		{Delete: &raft.DeleteCommand{Key: "b"}},
	}, to.ops)
	require.Equal(t, time.Second, to.timeout)
}

// AND THE ORDER IS THE CALLER'S, however many rows there are.
func TestTheRowsComeOutInTheOrderTheyWentIn(t *testing.T) {
	e := row.Sized(4)
	for _, key := range []string{"a", "b", "c", "d"} {
		e.Set(key, nil)
	}

	var to applied
	require.NoError(t, e.Apply(&to, 0))

	var keys []string
	for _, op := range to.ops {
		keys = append(keys, op.Set.Key)
	}
	require.Equal(t, []string{"a", "b", "c", "d"}, keys)
}

// THE ZERO VALUE IS THE ONE A CALLER CAN WRITE FOR ITSELF, and it holds nothing:
// the slice is unexported, so there is no field to put a row in. What it can do
// is be added to through the two verbs, which is the only way any row exists.
func TestTheOnlyEntryAcallerCanBuildForItselfHoldsNothing(t *testing.T) {
	var e row.Entry
	require.Zero(t, e.Len())

	var to applied
	require.ErrorIs(t, e.Apply(&to, time.Second), row.ErrNoRows)
	require.Zero(t, to.calls, "an entry with no rows must not reach the store at all")
}

// A ROW GOES IN AND CANNOT COME BACK OUT except at the store. There is no
// accessor and no exported field, so a caller cannot read a key back to
// re-splice it, and cannot hand the batch anywhere Apply did not.
func TestApplyIsTheOnlyWayOut(t *testing.T) {
	e := row.Sized(1)
	e.Set("k", []byte("v"))

	to := applied{err: errors.New("log is full")}
	require.ErrorContains(t, e.Apply(&to, time.Second), "log is full")
	require.Equal(t, 1, to.calls)

	// AND APPLYING TWICE IS TWO BATCHES, not an entry that empties itself. The
	// caller owns the value; what it cannot do is change what is in it.
	to.err = nil
	require.NoError(t, e.Apply(&to, time.Second))
	require.Equal(t, 2, to.calls)
	require.Equal(t, 1, e.Len())
}

// A COPY SHARES THE ORIGINAL'S ROWS, which is the one thing about this value a
// caller has to know, so it is measured rather than assumed. An Entry is an
// ordinary struct over a slice: two variables holding one entry are one entry,
// and rows added through either can overwrite the other's.
//
// ITS CALLER HAS ONE PER ENTRY AND KEEPS IT BEHIND A POINTER — the row methods
// hang off *entry, and the type that chains them carries that pointer — so no
// copy is made there, and population_test.go pins that the entry is assembled in
// exactly one place. This test exists so that the sharing is a stated property
// rather than a surprise somebody debugs.
func TestACopySharesTheRowsAndIsNotASecondEntry(t *testing.T) {
	e := row.Sized(4)
	e.Set("shared", nil)

	other := e
	other.Set("second", nil)
	e.Set("also second", nil)

	var to applied
	require.NoError(t, other.Apply(&to, 0))
	require.Equal(t, []string{"shared", "also second"}, keysOf(to.ops),
		"one entry, whichever variable named it")
}

func keysOf(ops []raft.BatchOp) []string {
	var out []string
	for _, op := range ops {
		out = append(out, op.Set.Key)
	}
	return out
}

// sink is what keeps the measurement below honest: Apply hands the ops to an
// interface's method, so the slice escapes in the real path too.
var (
	sink   []raft.BatchOp
	legacy *legacyEntry
	held   *heldEntry
)

type keeper struct{}

func (keeper) Batch(ops []raft.BatchOp, _ time.Duration) error { sink = ops; return nil }

// legacyOp and legacyOps are the shape round ten deleted, kept here so that the
// trade is measured rather than argued: a row was an ordinary value in an
// ordinary slice, and the slice was COPIED into raft's own shape on the way out
// — which is the copy that made the store's door take an expression.
type legacyOp struct{ op raft.BatchOp }

// Both arms hold their rows in a HEAP STRUCT, because that is where a caller's
// entry lives: one per transition, handed down a chain of row methods. Measured
// into a local the slice does not escape, and the allocation this round removed
// reads as zero.
type legacyEntry struct{ ops []legacyOp }

type heldEntry struct{ ops row.Entry }

func legacyOps(rows []legacyOp) []raft.BatchOp {
	out := make([]raft.BatchOp, len(rows))
	for i, r := range rows {
		out[i] = r.op
	}
	return out
}

// WHAT AN ENTRY COSTS, on the widest one its caller writes: four rows composed
// and handed to a store, once per replicated entry. It is printed rather than
// asserted, because what it has to be smaller than is a raft round trip and
// nothing here can bound one.
//
// The two are the same four rows either way. What the second one does not do is
// allocate the second slice — the rows are built into raft's own shape as they
// go — so what the round removed from the door is also one allocation per entry.
func BenchmarkEntry(b *testing.B) {
	value := make([]byte, 128)
	sha := make([]byte, 40)
	var to keeper

	b.Run("the rows and the copy they replaced", func(b *testing.B) {
		for b.Loop() {
			e := &legacyEntry{ops: make([]legacyOp, 0, 4)}
			e.ops = append(e.ops,
				legacyOp{op: raft.BatchOp{Set: &raft.SetCommand{Key: "build/job/AAA/record", Value: value}}},
				legacyOp{op: raft.BatchOp{Delete: &raft.DeleteCommand{Key: "build/index/open/BBB/AAA"}}},
				legacyOp{op: raft.BatchOp{Set: &raft.SetCommand{Key: "build/index/terminal/00000000001756000000/AAA"}}},
				legacyOp{op: raft.BatchOp{Set: &raft.SetCommand{Key: "build/index/ref/BBB/CCC/last", Value: sha}}},
			)
			legacy = e
			if err := to.Batch(legacyOps(e.ops), 0); err != nil {
				b.Fatal(err)
			}
		}
		if len(sink) != 4 {
			b.Fatal("the compiler elided the thing being measured")
		}
	})

	b.Run("an entry", func(b *testing.B) {
		for b.Loop() {
			e := &heldEntry{ops: row.Sized(4)}
			e.ops.Set("build/job/AAA/record", value)
			e.ops.Delete("build/index/open/BBB/AAA")
			e.ops.Set("build/index/terminal/00000000001756000000/AAA", nil)
			e.ops.Set("build/index/ref/BBB/CCC/last", sha)
			held = e
			if err := e.ops.Apply(to, 0); err != nil {
				b.Fatal(err)
			}
		}
		if len(sink) != 4 {
			b.Fatal("the compiler elided the thing being measured")
		}
	})
}
