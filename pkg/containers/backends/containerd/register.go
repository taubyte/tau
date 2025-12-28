//go:build linux || darwin || windows

// This file ensures containerd backend is available on all platforms

package containerd

import (
	"github.com/taubyte/tau/pkg/containers"
)

func init() {
	containers.RegisterBackend(containers.BackendTypeContainerd, func(config containers.ContainerdConfig) (containers.Backend, error) {
		return NewContainerdBackend(config)
	})
}
