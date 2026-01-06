package containers

import (
	"github.com/docker/docker/api/types/filters"
	"github.com/taubyte/tau/pkg/containers/core"
)

// New Filter returns a filter argument to perform key value Lookups on docker host.
func NewFilter(key, value string) filters.Args {
	filter := filters.NewArgs()
	filter.Add(key, value)

	return filter
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
			config.Volumes[i] = core.VolumeMount{
				Source:       vol.source,
				Destination:  vol.target,
				ReadOnly:     false, // Old API doesn't support read-only
				IsVolumeName: false, // Old API uses bind mounts
			}
		}
	}

	return config
}
