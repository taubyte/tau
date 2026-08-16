package builder

import (
	"context"
	"io"
	"os"
	"runtime"

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
	dev             bool
}

// restrictEgress reports whether this build's containers should be confined to
// the public internet.
//
// Only off a development node, and only on Linux. The enforcement is a host
// firewall (nftables plus cgroups), so on any other OS there is nothing to
// enforce with, and a development node — a laptop, a dream universe — has
// neither the privileges to program one nor an infrastructure worth reaching.
// Asking there would fail every build closed for no gain.
func (b *builder) restrictEgress() bool {
	return !b.dev && runtime.GOOS == "linux"
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
