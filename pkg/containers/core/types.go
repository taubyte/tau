// Package core provides core interfaces and types for container backends
// This package is separate from the main containers package to avoid import cycles
package core

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	// ErrBuildNotSupported is returned when a backend doesn't support building images
	ErrBuildNotSupported = errors.New("build not supported by this backend")
)

// ContainerID is a type-safe identifier for containers
type ContainerID string

// BackendType identifies which backend a configuration or build input is for
type BackendType string

const (
	BackendTypeContainerd BackendType = "containerd"
	BackendTypeDocker     BackendType = "docker"
)

// Backend defines the interface for container runtime backends
type Backend interface {
	// BackendType returns which backend this is (docker, containerd, etc.)
	BackendType() BackendType
	// Image operations - returns an Image interface for image management
	Image(name string) Image

	// Container operations
	Create(ctx context.Context, config *ContainerConfig) (ContainerID, error)
	Start(ctx context.Context, id ContainerID) error
	Stop(ctx context.Context, id ContainerID) error
	Remove(ctx context.Context, id ContainerID) error
	// Wait blocks until the container exits. The container's own exit status is
	// not an error: it is reported by Inspect. Wait errors only when waiting
	// itself fails.
	Wait(ctx context.Context, id ContainerID) error
	// Logs returns the container's output as a docker-multiplexed stream, to be
	// demultiplexed with stdcopy. Callers must read it before removing the container.
	Logs(ctx context.Context, id ContainerID) (io.ReadCloser, error)
	Inspect(ctx context.Context, id ContainerID) (*ContainerInfo, error)

	// Clean removes images older than age. A build node pulls a new image for
	// every toolchain version it sees, so without this its disk fills up.
	//
	// reference scopes the sweep to one image reference; empty means every
	// image the backend can see, including ones tau never pulled.
	Clean(ctx context.Context, age time.Duration, reference string) error

	// Backend-specific operations
	HealthCheck(ctx context.Context) error
	Capabilities() BackendCapabilities
}

// Image defines the interface for image operations
type Image interface {
	// Pull retrieves the image from a registry/repository
	Pull(ctx context.Context) error
	// Build builds the image from a Dockerfile.
	// Returns ErrBuildNotSupported if the backend cannot build.
	Build(ctx context.Context, input *DockerfileBuild) error
	// Exists checks if the image exists locally
	Exists(ctx context.Context) bool
	// Remove removes the image from the backend
	Remove(ctx context.Context) error
	// Name returns the image name/identifier
	Name() string
	// Digest returns the image digest
	Digest(ctx context.Context) (string, error)
	// Tags returns all tags for this image
	Tags(ctx context.Context) ([]string, error)
}

// DockerfileBuild is everything needed to build an image from a Dockerfile:
// a tar of the build context, and the name of the Dockerfile within it.
type DockerfileBuild struct {
	// Context is a tar stream of the build directory.
	Context io.Reader
	// Dockerfile is the path within the context; empty means "Dockerfile".
	Dockerfile string
}

// DockerfileName returns the Dockerfile to build, defaulting to "Dockerfile".
func (d *DockerfileBuild) DockerfileName() string {
	if d.Dockerfile == "" {
		return "Dockerfile"
	}
	return d.Dockerfile
}

// BackendCapabilities describes what a backend supports
type BackendCapabilities struct {
	// SupportsBuild reports whether the backend can build an image from a
	// Dockerfile. containerd cannot without BuildKit, so callers building
	// images must check this first.
	SupportsBuild bool
}

// ContainerInfo contains information about a container
type ContainerInfo struct {
	ID        ContainerID
	Status    string
	ExitCode  int
	StartedAt time.Time
	Image     string
	Resources *ResourceLimits
}

// ResourceLimits defines resource constraints for containers
type ResourceLimits struct {
	Memory     int64 // Memory limit in bytes
	MemorySwap int64 // Total memory + swap limit in bytes (-1 for unlimited swap)
	CPUQuota   int64 // CPU quota in microseconds
	CPUPeriod  int64 // CPU period in microseconds
	CPUShares  int64 // CPU shares (relative weight)
	PIDs       int64 // Maximum number of PIDs
}

// ContainerConfig holds all configuration for creating a container
type ContainerConfig struct {
	Image     string
	Command   []string
	Env       []string
	WorkDir   string
	Volumes   []VolumeMount  // Unified volume mounts
	Network   *NetworkConfig // Unified network configuration
	Resources *ResourceLimits

	// Privileges a container needs beyond the runtime's defaults. Nothing but
	// the image build sets these today: BuildKit has to mount filesystems to
	// assemble layers, which the default sandbox forbids. They are here rather
	// than hidden inside the build path because they describe the container,
	// and a workload imported from a PaaS may legitimately need them too.
	Privileges *Privileges
}

// Privileges loosens a container's default confinement. Every field widens what
// the container may do, so leave it nil unless something demonstrably needs it.
type Privileges struct {
	// Capabilities to add, named without the CAP_ prefix (e.g. "SYS_ADMIN").
	Capabilities []string

	// Devices from the host to expose, by path (e.g. "/dev/fuse").
	Devices []string

	// Unconfined drops the runtime's default seccomp and AppArmor profiles.
	//
	// ponytail: one switch for both, because the only caller wants both off.
	// Split into per-profile settings if anything ever needs finer control.
	Unconfined bool

	// Privileged removes essentially all confinement. Only ask for it where the
	// runtime is already running as real root, since there the container's
	// confinement is the only thing being relaxed and nothing new is granted;
	// under a rootless runtime the user namespace is the real boundary and
	// narrower privileges belong there instead.
	Privileged bool
}

// VolumeMount represents a volume mount configuration
type VolumeMount struct {
	Source      string // Source path on host OR volume name when IsVolumeName
	Destination string // Destination path in container
	ReadOnly    bool   // Whether the mount is read-only

	// IsVolumeName treats Source as a named volume rather than a host path.
	// Docker only: containerd has no volume manager and rejects these.
	IsVolumeName bool
}

// NetworkConfig represents unified network configuration
type NetworkConfig struct {
	// Network mode: bridge, host, none, custom.
	// The containerd backend runs without CNI and accepts only host and none.
	Mode string

	// Port mappings: host port -> container port. Docker only.
	PortMappings []PortMapping

	// DNS servers
	DNS []string
}

// PortMapping represents a port mapping
type PortMapping struct {
	HostPort      int
	ContainerPort int
	Protocol      string // "tcp", "udp"
	HostIP        string // Optional: bind to specific host IP
}
