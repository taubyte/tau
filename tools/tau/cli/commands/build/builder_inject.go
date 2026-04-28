package build

import (
	"context"
	"io"

	"github.com/taubyte/tau/core/builders"
	build "github.com/taubyte/tau/pkg/builder"
)

// newBuilder creates a builder for the given workDir. Tests can override newBuilderFunc to inject a mock.
//
// The builder bind-mounts workDir into the build container as writable, so build
// images can write back into the user's source tree. To keep local builds
// predictable, we copy workDir into a temp sandbox and build against that. The
// sandbox is removed when the returned Builder is Closed.
var newBuilderFunc = func(ctx context.Context, output io.Writer, workDir string) (builders.Builder, error) {
	sandbox, cleanup, err := sandboxSource(workDir)
	if err != nil {
		return nil, err
	}
	b, err := build.New(ctx, output, sandbox)
	if err != nil {
		cleanup()
		return nil, err
	}
	return &sandboxedBuilder{Builder: b, cleanup: cleanup}, nil
}

type sandboxedBuilder struct {
	builders.Builder
	cleanup func()
}

func (s *sandboxedBuilder) Close() error {
	err := s.Builder.Close()
	s.cleanup()
	return err
}
