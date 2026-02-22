package build

import (
	"context"
	"io"

	"github.com/taubyte/tau/core/builders"
	build "github.com/taubyte/tau/pkg/builder"
)

// newBuilder creates a builder for the given workDir. Tests can override newBuilderFunc to inject a mock.
var newBuilderFunc = func(ctx context.Context, output io.Writer, workDir string) (builders.Builder, error) {
	return build.New(ctx, output, workDir)
}
