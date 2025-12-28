package containers

import (
	"io"

	"github.com/docker/docker/client"
)

var (
	ForceRebuild = false
)

// MuxedReadCloser wraps the Read/Close methods for muxed logs.
type MuxedReadCloser struct {
	reader io.ReadCloser
}

// Client wraps the methods of the docker Client.
type Client struct {
	*client.Client
	progressOutput bool
}

// volume defines the source and target to be volumed in the docker container.
type volume struct {
	source string
	target string
}

// Container wraps the methods of the docker container.
type Container struct {
	image   *DockerImage
	id      string
	cmd     []string
	shell   []string
	volumes []volume
	env     []string
	workDir string
}

// DockerImage wraps the methods of the docker image.
type DockerImage struct {
	client       *Client
	image        string
	buildTarball io.Reader
	output       io.Writer
}

type PullStatus struct {
	Status         string `json:"status"`
	ProgressDetail struct {
		Current int `json:"current"`
		Total   int `json:"total"`
	} `json:"progressDetail"`
	Id          string `json:"id"`
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

type BuildStatus struct {
	Stream      string `json:"stream"`
	Error       string `json:"error"`
	ErrorDetail struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
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
