package transform

import (
	"context"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
)

type Context[T object.DataTypes] interface {
	context.Context
	Fork(object.Object[T]) Context[T] // forks keep same store
	Path() []any                      // ref added at each fork
	Store() Store[T]
}

type Store[T object.DataTypes] interface {
	String(string) Item[string]
	Bytes(string) Item[[]byte]
	Object(string) Item[object.Object[T]]
	Validators() Item[[]engine.NextValidation]
}

type Item[T any] interface {
	Exist() bool
	Get() T
	Set(T) (T, error)
	Del() error
}

type Transformer[T object.DataTypes] interface {
	Process(Context[T], object.Object[T]) (object.Object[T], error)
}
