package kvdb

import (
	"context"
	"time"

	cid "github.com/ipfs/go-cid"
)

type Instance interface {
	// Get will retrieve the key indexed data
	Get(ctx context.Context, key string) ([]byte, error)

	// Put will insert the data, indexed by key
	Put(ctx context.Context, key string, v []byte) error

	// Delete deletes the key and index data
	Delete(ctx context.Context, key string) error

	// List will list all keys with the given prefix
	List(ctx context.Context, prefix string) ([]string, error)

	// ListAsync returns a channel to list to listed keys
	ListAsync(ctx context.Context, prefix string) (chan string, error)

	// ListRegex will list all keys matching the given prefix, and regexs
	ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error)

	// ListRegexAsync will return a channel to list all regex matched keys
	ListRegExAsync(ctx context.Context, prefix string, regexs ...string) (chan string, error)

	// Batch creates a Batch interface of the current Instance
	Batch(ctx context.Context) (Batch, error)

	// Sync syncs the Instance key values
	Sync(ctx context.Context, key string) error

	Stats(ctx context.Context) Stats

	// Closes the Instance
	Close()
}

// Compactor is an Instance that can fold its history into a snapshot of its live
// state, returning how many history blocks it dropped. It is separate from Instance
// so that a store without history, such as a mock, is still an Instance.
type Compactor interface {
	Compact(ctx context.Context) (int, error)
}

// Retainer is an Instance whose history is kept for a retention T, which can be moved
// while it is open. A store without history, such as a mock, has none.
type Retainer interface {
	// CanRetain is whether this database can write under t, without changing it.
	CanRetain(t time.Duration) error
	// SetRetention changes T for what this database writes from now on.
	SetRetention(t time.Duration) error
}

type Batch interface {
	Put(key string, value []byte) error
	Delete(key string) error
	Commit() error
}

type Type uint

const (
	TypeCRDT Type = iota
)

type Stats interface {
	Type() Type
	Heads() []cid.Cid
}
