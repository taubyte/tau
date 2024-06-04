package builder

import (
	"context"
	"os"

	ci "github.com/taubyte/go-simple-container"
	"github.com/taubyte/tau/pkg/specs/builders"
)

// builder wraps the methods of the Builder interface
type builder struct {
	config          *builders.Config
	wd              builders.Dir
	containerClient *ci.Client
	context         context.Context
	tarball         []byte
}

// output wraps the methods of the Output interface
type output struct {
	logs   logs
	wd     builders.Dir
	outDir string
}

type logs struct {
	*os.File
}
