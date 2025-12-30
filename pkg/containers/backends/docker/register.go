package docker

import (
	"github.com/taubyte/tau/pkg/containers"
)

func init() {
	containers.RegisterBackend(containers.BackendTypeDocker, func(config containers.DockerConfig) (containers.Backend, error) {
		return New(config)
	})
}
