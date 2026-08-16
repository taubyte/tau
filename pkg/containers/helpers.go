package containers

import (
	"github.com/taubyte/tau/pkg/containers/core"
)

// convertToContainerConfig converts old Container options to ContainerConfig
func convertToContainerConfig(imageName string, c *Container) *core.ContainerConfig {
	config := &core.ContainerConfig{
		Image:   imageName,
		Command: c.cmd,
		Env:     c.env,
		WorkDir: c.workDir,
	}

	if c.restrictedEgress {
		if config.Network == nil {
			config.Network = &core.NetworkConfig{}
		}
		config.Network.RestrictEgress = true
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
