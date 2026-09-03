// Package row is THE ROWS OF ONE REPLICATED ENTRY, and it is a package rather
// than a type in its caller for one reason: neither a row nor the batch it ends
// up in MUST BE SPELLABLE anywhere but here.
//
// # What it is for
//
// Its caller (services/build/backend) spent ten rounds making a write
// impossible to make outside the transition that decided it, about the job that
// transition read. Every round moved a DOOR — a token, a key, an id, an op —
// and every round the thing that survived was A CHECK ON AN ARGUMENT, defeated
// the next time by respelling the argument:
//
//	round 9  a row was an ordinary raft.BatchOp, so one could be composed from
//	         scratch beside the two functions that made one:
//
//	           s.ops = append(s.ops, raft.BatchOp{Set: &raft.SetCommand{
//	                   Key: refLastKey("github.com/evil/repo", …)}})
//
//	         The answer was an Op whose field is unexported here. That line
//	         stopped compiling.
//
//	round 10 THE OP WAS GONE AND THE SLICE WAS STILL THERE. []Op was an
//	         ordinary slice and Ops([]Op) []raft.BatchOp was an ordinary call,
//	         so the store's door still took an EXPRESSION and the guard on it
//	         read the outermost callee's NAME:
//
//	           store.Batch(append(Ops(e.ops), forged), t)  -> "append", caught
//	           store.Batch(Ops(append(e.ops, forged)), t)  -> "Ops",    GREEN
//
//	         Same rows, same store, one word moved. So there is no []Op, no Op
//	         and no Ops: the rows are a struct with an unexported slice, a row
//	         is MADE INTO one rather than made and then put in one, and the
//	         thing that hands them to a store TAKES NO ROWS.
//
// # What that leaves a caller
//
//	Entry{}                     compiles, and holds no rows: ops is unexported
//	e.Set(key, value)           the only way a row with a value comes to exist
//	e.Delete(key)               the only way a row removed comes to exist
//	e.Apply(store, timeout)     the only way any of them reaches a store
//
// and nothing else. There is no expression in the caller that has the type
// raft's Batch takes, so there is nothing to append to on the way out, nothing
// to splice in beside, and no second spelling for a guard to miss. A caller
// that wants a raw op at the store has to write a Batch call of its own, which
// is a name its caller enumerates and says out loud that it enumerates.
//
// # What it deliberately is not
//
// It is not a key builder and it does not validate one. WHICH key a row may
// name is its caller's question — the key must be composed from the subject the
// section was entered on, which is a fact about that package's handle and means
// nothing here. This package answers only "can a row, or a batch of them, be
// made anywhere but through the four calls above", and the answer is no.
package row

import (
	"errors"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// Batcher is what applies an entry: pkg/raft's Cluster, and its caller's Store,
// method for method. It is named here so that Apply below can take the store as
// an ARGUMENT rather than the ops — which is the whole of round ten.
type Batcher interface {
	Batch(ops []raft.BatchOp, timeout time.Duration) error
}

// ErrNoRows is an entry applied with nothing in it: a replicated round trip for
// a transition that decided nothing. It is refused rather than sent, because a
// batch of no rows is indistinguishable at the store from one that was applied.
var ErrNoRows = errors.New("row: an entry with no rows")

// Entry is the rows of ONE replicated entry.
//
// THE SLICE IS UNEXPORTED AND SO IS EVERY ELEMENT'S CONTENT. Everything else
// about it — that it is an ordinary value its caller keeps in a field, copies
// and counts — is deliberate: what a caller cannot do is put anything in one
// that did not come from Set or Delete, take anything out, or name the batch
// that leaves.
type Entry struct{ ops []raft.BatchOp }

// Sized is an entry that will not regrow the slice for its first n rows. It is
// the one place a caller's own idea of how wide an entry gets is spent.
func Sized(n int) Entry { return Entry{ops: make([]raft.BatchOp, 0, n)} }

// Set adds a row with a value at a key.
func (e *Entry) Set(key string, value []byte) {
	e.ops = append(e.ops, raft.BatchOp{Set: &raft.SetCommand{Key: key, Value: value}})
}

// Delete adds a row removed.
func (e *Entry) Delete(key string) {
	e.ops = append(e.ops, raft.BatchOp{Delete: &raft.DeleteCommand{Key: key}})
}

// Len is how many rows went in. A caller that writes exactly one row can say so
// by counting rather than by trusting the branch it took — which is a fact
// about what this entry HOLDS and not about how anything was spelled.
func (e Entry) Len() int { return len(e.ops) }

// Apply is THE ONE WAY OUT, and it takes the store rather than the rows.
//
// Its callers are counted in the package that uses this one: they are the two
// places that reach a replicated batch at all, and a third would be a write no
// section decided.
func (e Entry) Apply(to Batcher, timeout time.Duration) error {
	if len(e.ops) == 0 {
		return ErrNoRows
	}
	return to.Batch(e.ops, timeout)
}
