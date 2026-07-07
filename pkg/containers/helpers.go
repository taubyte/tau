package containers

import (
	"github.com/moby/moby/client"
	"github.com/taubyte/tau/pkg/containers/core"
)

// New Filter returns a filter argument to perform key value Lookups on docker host.
func NewFilter(key, value string) client.Filters {
	return client.Filters{}.Add(key, value)
}

// convertToContainerConfig converts old Container options to ContainerConfig
func convertToContainerConfig(imageName string, c *Container) *core.ContainerConfig {
	config := &core.ContainerConfig{
		Image:   imageName,
		Command: c.cmd,
		Shell:   c.shell,
		Env:     c.env,
		WorkDir: c.workDir,
	}

	// Convert volumes
	if len(c.volumes) > 0 {
		config.Volumes = make([]core.VolumeMount, len(c.volumes))
		for i, vol := range c.volumes {
			// Old API doesn't support read-only; it uses bind mounts (not volume names)
			config.Volumes[i] = core.VolumeMount{
				Source:      vol.source,
				Destination: vol.target,
			}
		}
	}

	return config
}
