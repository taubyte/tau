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
	BackendTypeContainerd  BackendType = "containerd"
	BackendTypeDocker      BackendType = "docker"
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

// ResourceLimits defines resource constraints for containers
type ResourceLimits struct {
	Memory     int64 // Memory limit in bytes
	MemorySwap int64 // Total memory + swap limit in bytes (-1 for unlimited swap)
	CPUQuota   int64 // CPU quota in microseconds
	CPUPeriod  int64 // CPU period in microseconds
	CPUShares  int64 // CPU shares (relative weight)
	Storage    int64 // Storage limit in bytes
	PIDs       int64 // Maximum number of PIDs
}

// ContainerConfig holds all configuration for creating a container
type ContainerConfig struct {
	Image     string
	Command   []string
	Shell     []string
	Env       []string
	WorkDir   string
	Volumes   []VolumeMount  // Unified volume mounts
	Network   *NetworkConfig // Unified network configuration
	Resources *ResourceLimits
}

// VolumeMount represents a volume mount configuration
type VolumeMount struct {
	Source      string // Source path on host OR volume name (for OPS/Nanos)
	Destination string // Destination path in container
	ReadOnly    bool   // Whether the mount is read-only

	// OPS/Nanos specific: if true, Source is treated as volume name
	// Format: "volume_name:/mount/path" (for OPS volume mounting)
	// If false, Source is a host path (standard bind mount)
	IsVolumeName bool
}

// NetworkConfig represents unified network configuration
type NetworkConfig struct {
	// Network mode: bridge, host, none, custom
	// For OPS/Nanos: maps to hypervisor network type (QEMU/Xen)
	Mode string

	// Port mappings: host port -> container port
	PortMappings []PortMapping

	// DNS servers
	DNS []string

	// Network aliases (for containerd/Docker)
	Aliases []string

	// Custom network name (for custom mode)
	NetworkName string

	// IP Configuration (for OPS/Nanos, Firecracker on bare metal)
	IPConfig *IPConfig

	// MTU size (for OPS/Nanos)
	MTU int

	// Additional backend-specific options
	BackendOptions map[string]interface{}
}

// IPConfig represents IP address configuration
type IPConfig struct {
	// IPv4 configuration
	IPv4 *IPv4Config

	// IPv6 configuration
	IPv6 *IPv6Config
}

// IPv4Config represents IPv4 settings
type IPv4Config struct {
	// Static IP address (if empty, uses DHCP)
	Address string

	// Wait for DHCP (seconds, for OPS/Nanos)
	WaitForDHCPSeconds int

	// Gateway
	Gateway string

	// Subnet mask
	Netmask string
}

// IPv6Config represents IPv6 settings
type IPv6Config struct {
	// Static IPv6 address (if empty, uses DHCPv6)
	Address string

	// Wait for DHCPv6 (seconds, for OPS/Nanos)
	WaitForDHCPSeconds int

	// Gateway
	Gateway string
}

// PortMapping represents a port mapping
type PortMapping struct {
	HostPort      int
	ContainerPort int
	Protocol      string // "tcp", "udp"
	HostIP        string // Optional: bind to specific host IP
}
