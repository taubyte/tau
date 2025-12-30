//go:build linux

// This file ensures containerd backend is available on Linux

package containerd

import (
	"github.com/taubyte/tau/pkg/containers"
)

func init() {
	containers.RegisterBackend(containers.BackendTypeContainerd, func(config containers.ContainerdConfig) (containers.Backend, error) {
		return New(config)
	})
}
