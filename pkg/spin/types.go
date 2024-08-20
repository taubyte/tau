package runtime

import "context"

type Spin interface {
	New(options ...Option[Container]) (Container, error)

	Close()
}

type Container interface {
	Run() error
	Stop()
}

type Registry interface {
	Pull(ctx context.Context, image string) error
	Path(image string) (string, error)
	Close()
}

type Option[T any] func(T) error
