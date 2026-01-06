package docker

import (
	"fmt"

	"github.com/taubyte/tau/pkg/containers/core"
)

func init() {
	core.RegisterBackend(core.BackendTypeDocker, func(config core.BackendConfig) (core.Backend, error) {
		dockerConfig, ok := config.(core.DockerConfig)
		if !ok {
			return nil, fmt.Errorf("expected DockerConfig, got %T", config)
		}
		return New(dockerConfig)
	})
}
