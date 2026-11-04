// Package row is the rows of one replicated entry, a package so that neither a row
// nor the batch it ends up in is spellable anywhere else:
//
//	Entry{}                     holds no rows: ops is unexported
//	e.Set(key, value)           the only way a row with a value comes to exist
//	e.Delete(key)               the only way a removed row comes to exist
//	e.Apply(ctx, store)         the only way any of them reaches a store
//
// An entry is ONE kvdb delta: every row in it reaches every replica together or
// not at all. There is no expression of the batch in a caller, so nothing can be
// appended or spliced on the way out. Which key a row may name is the caller's
// question.
package row

import (
	"context"
	"errors"

	"github.com/taubyte/tau/core/kvdb"
)

// Batcher is what applies an entry (a kvdb database), named so Apply takes the
// store rather than the rows.
type Batcher interface {
	Batch(ctx context.Context) (kvdb.Batch, error)
}

// ErrNoRows is an entry applied with nothing in it: a replicated delta for a
// transition that decided nothing. It is refused rather than sent, because an
// empty delta is indistinguishable at the store from one that was applied.
var ErrNoRows = errors.New("row: an entry with no rows")

// op is one row: a value at a key, or the key removed.
type op struct {
	key    string
	value  []byte
	delete bool
}

// Entry is the rows of one replicated entry; nothing can be put in except via Set
// or Delete, taken out, or named as a batch.
type Entry struct{ ops []op }

// Sized is an entry that will not regrow the slice for its first n rows. It is
// the one place a caller's own idea of how wide an entry gets is spent.
func Sized(n int) Entry { return Entry{ops: make([]op, 0, n)} }

// Set adds a row with a value at a key.
func (e *Entry) Set(key string, value []byte) {
	e.ops = append(e.ops, op{key: key, value: value})
}

// Delete adds a row removed.
func (e *Entry) Delete(key string) {
	e.ops = append(e.ops, op{key: key, delete: true})
}

// Len is how many rows went in, so a caller can count rather than trust its
// branch.
func (e Entry) Len() int { return len(e.ops) }

// Apply is the one way out, taking the store rather than the rows; its callers are
// counted by the using package.
func (e Entry) Apply(ctx context.Context, to Batcher) error {
	if len(e.ops) == 0 {
		return ErrNoRows
	}
	b, err := to.Batch(ctx)
	if err != nil {
		return err
	}
	for _, o := range e.ops {
		if o.delete {
			err = b.Delete(o.key)
		} else {
			err = b.Put(o.key, o.value)
		}
		if err != nil {
			return err
		}
	}
	return b.Commit()
}
