// Package core holds the container backend interfaces and types, separate from
// pkg/containers to avoid import cycles.
package core

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrBuildNotSupported = errors.New("build not supported by this backend")
)

type ContainerID string

type BackendType string

const (
	BackendTypeContainerd BackendType = "containerd"
	BackendTypeDocker     BackendType = "docker"
)

type Backend interface {
	BackendType() BackendType
	Image(name string) Image

	Create(ctx context.Context, config *ContainerConfig) (ContainerID, error)
	Start(ctx context.Context, id ContainerID) error
	Stop(ctx context.Context, id ContainerID) error
	Remove(ctx context.Context, id ContainerID) error
	// Wait blocks until the container exits. The container's own exit status is
	// not an error: it is reported by Inspect. Wait errors only when waiting
	// itself fails.
	Wait(ctx context.Context, id ContainerID) error
	// Logs returns the container's output as a docker-multiplexed stream, to be
	// demultiplexed with stdcopy. It follows: output arrives as the container
	// writes it, from the first byte, and the stream ends when the container
	// exits. Callers must read it before removing the container.
	Logs(ctx context.Context, id ContainerID) (io.ReadCloser, error)
	Inspect(ctx context.Context, id ContainerID) (*ContainerInfo, error)

	// Clean removes images older than age. A build node pulls a new image for
	// every toolchain version it sees, so without this its disk fills up.
	//
	// reference scopes the sweep to one image reference; empty means every
	// image the backend can see, including ones tau never pulled.
	Clean(ctx context.Context, age time.Duration, reference string) error

	HealthCheck(ctx context.Context) error
	Capabilities() BackendCapabilities
}

type Image interface {
	Pull(ctx context.Context) error
	// Build returns ErrBuildNotSupported if the backend cannot build.
	Build(ctx context.Context, input *DockerfileBuild) error
	Exists(ctx context.Context) bool
	Remove(ctx context.Context) error
	Name() string
	Digest(ctx context.Context) (string, error)
	Tags(ctx context.Context) ([]string, error)
}

type DockerfileBuild struct {
	// RestrictEgress confines the build the same way NetworkConfig does for a
	// container. A Dockerfile is repository content, so its RUN steps are as
	// untrusted as anything else the repository ships.
	RestrictEgress bool

	Context io.Reader
	// Dockerfile is the path within the context; empty means "Dockerfile".
	Dockerfile string
}

func (d *DockerfileBuild) DockerfileName() string {
	if d.Dockerfile == "" {
		return "Dockerfile"
	}
	return d.Dockerfile
}

type BackendCapabilities struct {
	// SupportsBuild reports whether the backend can build an image from a
	// Dockerfile. containerd cannot without BuildKit, so callers building
	// images must check this first.
	SupportsBuild bool
}

type ContainerInfo struct {
	ID        ContainerID
	Status    string
	ExitCode  int
	StartedAt time.Time
	Image     string
	Resources *ResourceLimits
}

type ResourceLimits struct {
	Memory     int64 // Memory limit in bytes
	MemorySwap int64 // Total memory + swap limit in bytes (-1 for unlimited swap)
	CPUQuota   int64 // CPU quota in microseconds
	CPUPeriod  int64 // CPU period in microseconds
	CPUShares  int64 // CPU shares (relative weight)
	PIDs       int64 // Maximum number of PIDs
}

type ContainerConfig struct {
	Image     string
	Command   []string
	Env       []string
	WorkDir   string
	Volumes   []VolumeMount
	Network   *NetworkConfig
	Resources *ResourceLimits

	// Stdin is what the container's process reads on its standard input, then
	// EOF. Nil is a closed stdin, which is what a container nobody attached to
	// gets. It is read once, from Start, so a container given one is started once.
	Stdin io.Reader

	// Privileges a container needs beyond the runtime's defaults. Nothing but
	// the image build sets these today: BuildKit has to mount filesystems to
	// assemble layers, which the default sandbox forbids. They are here rather
	// than hidden inside the build path because they describe the container,
	// and a workload imported from a PaaS may legitimately need them too.
	Privileges *Privileges

	// Labels are attached to the container where the runtime has them, so
	// something that outlives this process can still find what it started.
	Labels map[string]string
}

// Privileges loosens a container's default confinement. Every field widens what
// the container may do, so leave it nil unless something demonstrably needs it.
type Privileges struct {
	// Capabilities to add, named without the CAP_ prefix (e.g. "SYS_ADMIN").
	Capabilities []string

	// Devices from the host to expose, by path (e.g. "/dev/fuse").
	Devices []string

	// Unconfined drops the runtime's default seccomp and AppArmor profiles. One switch
	// for both because the only caller wants both off; split it if something needs
	// finer control.
	Unconfined bool

	// Privileged removes essentially all confinement. Only ask for it where the
	// runtime is already running as real root, since there the container's
	// confinement is the only thing being relaxed and nothing new is granted;
	// under a rootless runtime the user namespace is the real boundary and
	// narrower privileges belong there instead.
	Privileged bool
}

type VolumeMount struct {
	Source      string // Source path on host OR volume name when IsVolumeName
	Destination string
	ReadOnly    bool

	// IsVolumeName treats Source as a named volume rather than a host path.
	// Docker only: containerd has no volume manager and rejects these.
	IsVolumeName bool
}

type NetworkConfig struct {
	// Network mode: bridge, host, none, custom.
	// The containerd backend runs without CNI and accepts only host and none.
	Mode string

	// Port mappings: host port -> container port. Docker only.
	PortMappings []PortMapping

	DNS []string

	// RestrictEgress makes the backend install a host firewall that blocks the
	// container from reaching node-local and private/link-local networks (the
	// cloud metadata endpoint, loopback, RFC1918) while still allowing the
	// public internet. Set for untrusted workloads. Fail-closed: if the firewall
	// cannot be installed (e.g. missing CAP_NET_ADMIN) the container is not
	// started.
	RestrictEgress bool
}

type PortMapping struct {
	HostPort      int
	ContainerPort int
	Protocol      string // "tcp", "udp"
	HostIP        string // Optional: bind to specific host IP
}
