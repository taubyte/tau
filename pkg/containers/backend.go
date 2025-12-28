// Package containers provides a unified interface for container runtime operations
package containers

import (
	"context"
	"io"
	"time"
)

// ContainerID is a type-safe identifier for containers
type ContainerID string

// BackendType identifies which backend a configuration or build input is for
type BackendType string

const (
	BackendTypeContainerd  BackendType = "containerd"
	BackendTypeFirecracker BackendType = "firecracker"
	BackendTypeNanos       BackendType = "nanos"
)

// Backend defines the interface for container runtime backends
type Backend interface {
	// Image operations - returns an Image interface for image management
	Image(name string) Image

	// Container operations
	Create(ctx context.Context, config *ContainerConfig) (ContainerID, error)
	Start(ctx context.Context, id ContainerID) error
	Stop(ctx context.Context, id ContainerID) error
	Remove(ctx context.Context, id ContainerID) error
	Wait(ctx context.Context, id ContainerID) error
	Logs(ctx context.Context, id ContainerID) (io.ReadCloser, error)
	Inspect(ctx context.Context, id ContainerID) (*ContainerInfo, error)

	// Backend-specific operations
	HealthCheck(ctx context.Context) error
	Capabilities() BackendCapabilities
}

// Image defines the interface for image operations
type Image interface {
	// Pull retrieves the image from a registry/repository
	Pull(ctx context.Context) error
	// Build builds an image from backend-specific inputs
	// Returns ErrBuildNotSupported if the backend doesn't support building
	// Input types vary by backend:
	//   - Containerd: ContainerdBuildInput (Dockerfile + build context, uses BuildKit)
	//   - OPS/Nanos: NanosBuildInput (application binary path + config)
	//   - Firecracker: FirecrackerBuildInput (kernel + rootfs paths)
	Build(ctx context.Context, input BuildInput) error
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

// BuildInput is a type that can hold different build inputs for different backends
// Backends can type-assert to get their specific input type
type BuildInput interface {
	// Type returns the backend type this input is for
	Type() BackendType
}

// BackendCapabilities describes what a backend supports
type BackendCapabilities struct {
	// Supported resource limits
	SupportsMemory     bool
	SupportsCPU        bool
	SupportsStorage    bool
	SupportsPIDs       bool
	SupportsMemorySwap bool

	// Supported features
	SupportsBuild      bool
	SupportsOCI        bool
	SupportsNetworking bool
	SupportsVolumes    bool
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
