package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/pbnjay/memory"
	ci "github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/containers/core"
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

// buildLimits caps a confined build so one build.sh cannot starve the node or
// its other tenants: buildMemory of RAM, buildSwap, 4096 processes, half the
// cores.
func buildLimits() core.ResourceLimits {
	const period = 100_000
	mem := buildMemory(memory.TotalMemory())
	return core.ResourceLimits{
		Memory:     mem,
		MemorySwap: buildSwap(mem),
		PIDs:       4096,
		CPUPeriod:  period,
		CPUQuota:   int64(max(runtime.NumCPU()/2, 1)) * period,
	}
}

// outOfMemory describes the limits tau set on this builder's builds.
func (b *builder) outOfMemory() OutOfMemoryError {
	if !b.restrictEgress() {
		return OutOfMemoryError{}
	}
	limits := buildLimits()
	return OutOfMemoryError{Limit: limits.Memory, Swap: limits.MemorySwap == -1}
}

// OutOfMemoryError is a build killed for using more memory than it may. Its
// message is what the build's owner reads.
type OutOfMemoryError struct {
	Limit int64 // bytes of RAM, 0 when the build was only limited by its machine
	Swap  bool  // the build could also use the node's swap
}

func (e OutOfMemoryError) Error() string {
	limit, node := "the memory of the machine building it", "a node with more memory"
	if e.Limit > 0 {
		limit = fmt.Sprintf("its limit of %.1f GiB", float64(e.Limit)/(1<<30))
		if e.Swap {
			limit += " of RAM plus the node's swap"
			node = "a node with more memory or swap"
		}
	}
	return "the build ran out of memory and was killed: it needed more than " + limit +
		". It was not retried, as it would fail the same way. Make the build use less memory at once (fewer parallel steps, a smaller heap such as NODE_OPTIONS=--max-old-space-size) or build on " + node
}

// How long a node builds with an image it pulled before asking the registry
// again: each check is a registry round trip, and anonymous Docker Hub pulls
// are rate limited. A new tag is a new name, so it is always pulled.
const (
	devReusePulled = 6 * time.Hour
	reusePulled    = time.Hour
)

func (b *builder) reusePulled() time.Duration {
	if b.dev {
		return devReusePulled
	}
	return reusePulled
}

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

const buildMemoryLimit = 4 << 30

// buildMemory is a build's memory limit: 4 GiB, but on a small node at most its
// memory less 1 GiB, so the build is killed before tau and Docker are. Never
// under 512 MiB; an unknown total gets the 4 GiB default.
func buildMemory(total uint64) int64 {
	const reserve, floor = 1 << 30, 512 << 20
	if total == 0 || total >= buildMemoryLimit+reserve {
		return buildMemoryLimit
	}
	return max(int64(total)-reserve, floor)
}

// buildSwap is a build's memory-plus-swap limit. A node too small to give a
// build the full limit lets it use the host's swap (-1), or large libraries
// cannot compile there at all; larger nodes keep builds to RAM.
func buildSwap(mem int64) int64 {
	if mem < buildMemoryLimit {
		return -1
	}
	return mem
}
