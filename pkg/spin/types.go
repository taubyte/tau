package spin

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
	Pull(ctx context.Context, image string, progress chan<- PullProgress) error
	Path(image string) (string, error)
	Close()
}

type PullProgress interface {
	Error() error
	Completion() int
}

type Option[T any] func(T) error
