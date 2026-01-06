// Package containers provides a unified interface for container runtime operations
package containers

import (
	"github.com/taubyte/tau/pkg/containers/core"
)

// Re-export core types for backward compatibility
type (
	Backend             = core.Backend
	Image               = core.Image
	BuildInput          = core.BuildInput
	BackendCapabilities = core.BackendCapabilities
	ContainerInfo       = core.ContainerInfo
	ContainerID         = core.ContainerID
	BackendType         = core.BackendType
	ContainerConfig     = core.ContainerConfig
	ResourceLimits      = core.ResourceLimits
	VolumeMount         = core.VolumeMount
	NetworkConfig       = core.NetworkConfig
	IPConfig            = core.IPConfig
	IPv4Config          = core.IPv4Config
	IPv6Config          = core.IPv6Config
	PortMapping         = core.PortMapping
)

// Re-export core constants
const (
	BackendTypeContainerd  = core.BackendTypeContainerd
	BackendTypeDocker      = core.BackendTypeDocker
	BackendTypeFirecracker = core.BackendTypeFirecracker
	BackendTypeNanos       = core.BackendTypeNanos
)
