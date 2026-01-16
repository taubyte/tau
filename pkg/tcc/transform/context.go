package transform

import (
	"context"

	"github.com/taubyte/tau/pkg/tcc/object"
)

type ctx[T object.DataTypes] struct {
	context.Context
	kv *store[T]
}

func NewContext[T object.DataTypes](c context.Context, root ...any) Context[T] {
	p := []any{}
	if c.Value(path) != nil {
		if val, ok := c.Value(path).([]any); ok {
			p = val
		}
	}
	return &ctx[T]{
		Context: context.WithValue(c, path, append(p, root...)),
		kv:      newStore[T](),
	}
}

func (c *ctx[T]) Store() Store[T] {
	return c.kv
}

var path struct{}

func (c *ctx[T]) Fork(this object.Object[T]) Context[T] {
	return &ctx[T]{
		Context: context.WithValue(c, path, append(c.Path(), this)),
		kv:      c.kv,
	}
}

func (c *ctx[T]) Path() []any {
	p := c.Value(path)
	if p == nil {
		return []any{}
	}
	if val, ok := p.([]any); ok {
		return val
	}
	return []any{}
}

type store[T object.DataTypes] struct {
	strings map[string]string
	bytes   map[string][]byte
	objects map[string]object.Object[T]
}

func newStore[T object.DataTypes]() *store[T] {
	return &store[T]{
		strings: make(map[string]string),
		bytes:   make(map[string][]byte),
		objects: make(map[string]object.Object[T]),
	}
}

func (s *store[T]) String(key string) Item[string] {
	return &item[string]{ds: s.strings, key: key}
}

func (s *store[T]) Bytes(key string) Item[[]byte] {
	return &item[[]byte]{ds: s.bytes, key: key}
}

func (s *store[T]) Object(key string) Item[object.Object[T]] {
	return &item[object.Object[T]]{ds: s.objects, key: key}
}

type item[T any] struct {
	ds  map[string]T
	key string
}

func (i item[T]) Get() T {
	return i.ds[i.key]
}

func (i item[T]) Exist() bool {
	_, ok := i.ds[i.key]
	return ok
}

func (i item[T]) Set(val T) (T, error) {
	i.ds[i.key] = val
	return val, nil
}

func (i item[T]) Del() error {
	delete(i.ds, i.key)
	return nil
}
