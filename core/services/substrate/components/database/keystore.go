package database

import "context"

type KV interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, v []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
	Close()
	UpdateSize(size uint64)
	Size(ctx context.Context) (uint64, error)
}
