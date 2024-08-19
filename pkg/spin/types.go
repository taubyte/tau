package spin

import "context"

type Spin interface {
	New(options ...Option[Container]) (Container, error)
	Pull(ctx context.Context, imageName, workPath, outputFilename string) (err error)

	Close()
}

type Container interface {
	Run() error
	Stop()
}

type Option[T any] func(T) error
