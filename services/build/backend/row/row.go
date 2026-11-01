// Package row is the rows of one replicated entry, a package so that neither a row
// nor the batch it ends up in is spellable anywhere else:
//
//	Entry{}                     holds no rows: ops is unexported
//	e.Set(key, value)           the only way a row with a value comes to exist
//	e.Delete(key)               the only way a removed row comes to exist
//	e.Apply(store, timeout)     the only way any of them reaches a store
//
// There is no expression of raft's batch type in a caller, so nothing can be
// appended or spliced on the way out. Which key a row may name is the caller's
// question.
package row

import (
	"errors"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// Batcher is what applies an entry (pkg/raft's Cluster), named so Apply takes the
// store rather than the ops.
type Batcher interface {
	Batch(ops []raft.BatchOp, timeout time.Duration) error
}

// ErrNoRows is an entry applied with nothing in it: a replicated round trip for
// a transition that decided nothing. It is refused rather than sent, because a
// batch of no rows is indistinguishable at the store from one that was applied.
var ErrNoRows = errors.New("row: an entry with no rows")

// Entry is the rows of one replicated entry; nothing can be put in except via Set
// or Delete, taken out, or named as a batch.
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

// Len is how many rows went in, so a caller can count rather than trust its
// branch.
func (e Entry) Len() int { return len(e.ops) }

// Apply is the one way out, taking the store rather than the rows; its callers are
// counted by the using package.
func (e Entry) Apply(to Batcher, timeout time.Duration) error {
	if len(e.ops) == 0 {
		return ErrNoRows
	}
	return to.Batch(e.ops, timeout)
}
