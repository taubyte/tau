package kvdb

import (
	"context"

	"github.com/ipfs/go-log/v2"

	cid "github.com/ipfs/go-cid"
)

type Factory interface {
	New(logger log.StandardLogger, path string, rebroadcastIntervalSec int) (s KVDB, err error)
	Close()
}

type KVDB interface {
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

	// Batch creates a Batch interface of the current KVDB
	Batch(ctx context.Context) (Batch, error)

	// Sync syncs the KVDB key values
	Sync(ctx context.Context, key string) error

	Factory() Factory

	Stats() Stats

	// Closes the KVDB
	Close()
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
	Encode() []byte // CBOR encoding
	Decode(data []byte) error
}
