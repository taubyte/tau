package builder

import (
	"context"
	"io"
	"os"
	"runtime"
	"time"

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

// devReusePulled is how long a development node builds with an image it pulled
// before asking the registry again; it saves a registry round trip per build.
const devReusePulled = 6 * time.Hour

// devCaches are the package-manager and compiler caches a development node
// keeps in named volumes shared by every build, so a build only downloads and
// compiles what the last one did not. Only on a development node: a cache
// shared across builds is one a build can poison for the next, and on a cloud
// those builds belong to different people. Every tool here locks its own cache,
// so concurrent builds may share them.
var devCaches = map[string]string{
	"tau-cache-gomod":   "/go/pkg/mod",
	"tau-cache-gobuild": "/root/.cache/go-build",
	"tau-cache-tinygo":  "/root/.cache/tinygo",
	"tau-cache-npm":     "/root/.npm",
	"tau-cache-yarn":    "/usr/local/share/.cache/yarn",
	"tau-cache-pnpm":    "/root/.local/share/pnpm/store",
	"tau-cache-cargo":   "/usr/local/cargo/registry",
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
