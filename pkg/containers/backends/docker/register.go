package docker

import (
	"fmt"

	"github.com/taubyte/tau/pkg/containers/core"
)

// Factory builds a docker backend from a DockerConfig.
func Factory(config core.BackendConfig) (core.Backend, error) {
	dockerConfig, ok := config.(core.DockerConfig)
	if !ok {
		return nil, fmt.Errorf("expected DockerConfig, got %T", config)
	}
	return New(dockerConfig)
}
