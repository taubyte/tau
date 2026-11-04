package instance

import (
	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/container"
)

// The worker's container tier, built over the daemon this machine declared
// (pkg/containers.Declared).

// containerTier builds the runtime over the daemon c declares. A function, not a
// method: config is inert data that constructs nothing (config_test.go's census).
func containerTier(c containers.Declared, disks container.Volumes, secrets container.Secrets) (invoke.Runtime, error) {
	backend, err := containers.Open(c)
	if err != nil {
		return nil, err
	}

	options := []container.Option{
		container.WithBackend(backend),
		// THE ONE THING THIS TIER CANNOT DO IS SAID OUT LOUD. A resource's
		// egress allowlist is not enforceable by an nftables floor, so an
		// operator whose author wrote one is told rather than left to assume
		// it took effect.
		container.WithWarn(func(format string, args ...any) { logger.Warnf(format, args...) }),
		container.WithSecrets(secrets),
	}
	// A NODE WITH NOWHERE TO PUT A DIRECTORY MOUNTS NOTHING, and the tier says
	// so on the workload that declared one rather than starting it with an
	// empty layer. See instance/volume.
	if disks != nil {
		options = append(options, container.WithVolumes(disks))
	}

	// THE SPEC ALREADY CARRIES THE ARTIFACT. A worker reads the image, the
	// memory limit and the environment off the compiled resource, so there is
	// nothing left for an image resolver to look up — container.ImageFromSpec
	// is that, and the injected ImageFunc stays for a registry mirror or a
	// local override.
	return container.New(container.ImageFromSpec, options...)
}
