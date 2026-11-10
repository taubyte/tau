//go:build linux

package containerd

import (
	"fmt"

	"github.com/taubyte/tau/pkg/containers/core"
)

// Factory builds a containerd backend from a ContainerdConfig.
func Factory(config core.BackendConfig) (core.Backend, error) {
	containerdConfig, ok := config.(core.ContainerdConfig)
	if !ok {
		return nil, fmt.Errorf("expected ContainerdConfig, got %T", config)
	}
	return New(containerdConfig)
}
