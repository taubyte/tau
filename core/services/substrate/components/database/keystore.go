package database

import "context"

type KV interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, v []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
	// ListRegEx lists the keys under prefix that match the patterns.
	ListRegEx(ctx context.Context, prefix string, regexs ...string) ([]string, error)
	// Batch applies ops in order in one commit: all of them are written or none.
	Batch(ctx context.Context, ops []Op) error
	Close()
	UpdateSize(size uint64)
	Size(ctx context.Context) (uint64, error)
}

// Op is one write of a Batch: Value is put at Key, or Key is deleted.
type Op struct {
	Key    string
	Value  []byte
	Delete bool
}
