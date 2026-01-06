//go:build linux

// This file ensures containerd backend is available on Linux

package containerd

import (
	"fmt"

	"github.com/taubyte/tau/pkg/containers/core"
)

func init() {
	core.RegisterBackend(core.BackendTypeContainerd, func(config core.BackendConfig) (core.Backend, error) {
		containerdConfig, ok := config.(core.ContainerdConfig)
		if !ok {
			return nil, fmt.Errorf("expected ContainerdConfig, got %T", config)
		}
		return New(containerdConfig)
	})
}
