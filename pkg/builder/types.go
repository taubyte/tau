package builder

import (
	"context"
	"io"
	"os"

	ci "github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/specs/builders"
)

// builder wraps the methods of the Builder interface
type builder struct {
	config          *builders.Config
	wd              builders.Dir
	containerClient *ci.Client
	context         context.Context
	tarball         []byte
	output          io.Writer
}

// output wraps the methods of the Output interface
type output struct {
	// logs   logs
	wd     builders.Dir
	outDir string
}

type logs struct {
	*os.File
}
