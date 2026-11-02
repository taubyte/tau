package node

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/container"
)

// Builds the container tier from what an operator declared: a tier is a live object,
// but which daemon a machine has is configuration.
//
// containerd's default handler is runc, where the host kernel is the only boundary.
// A node running others' code should name a sandboxed handler
// (`io.containerd.runsc.v1` for gVisor). Not defaulted: an uninstalled handler fails
// at container start, not boot.

// Backends this node knows how to build.
const (
	BackendDocker     = "docker"
	BackendContainerd = "containerd"
)

// ContainerConfig is the container tier an operator declared.
type ContainerConfig struct {
	// Backend is "docker", "containerd", or empty for none — a node whose
	// tiers are all injected, or one that runs no containers.
	Backend string

	// Socket is the daemon's address: a Docker host, or a containerd socket
	// path. Empty means each backend's own default.
	Socket string

	// Namespace is containerd's namespace. Ignored by Docker.
	Namespace string

	// Runtime is containerd's runtime handler — see this file's doc. Empty
	// means containerd's default, which is runc, which is not a sandbox.
	Runtime string
}

// ErrUnknownBackend is a backend name this node cannot build.
var ErrUnknownBackend = errors.New("node: not a container backend this node can build")

// containerBackend is the daemon an operator named, shared by the workload tier and
// the build engine so both use the same one.
func containerBackend(c ContainerConfig) (core.Backend, error) {
	var cfg core.BackendConfig

	switch c.Backend {
	case BackendDocker:
		cfg = core.DockerConfig{Host: c.Socket}
	case BackendContainerd:
		cfg = core.ContainerdConfig{
			SocketPath: c.Socket,
			Namespace:  c.Namespace,
			Runtime:    c.Runtime,
		}
	default:
		return nil, fmt.Errorf("%w: %q; it builds %s or %s",
			ErrUnknownBackend, c.Backend, BackendDocker, BackendContainerd)
	}

	backend, err := containers.NewBackend(cfg)
	if err != nil {
		return nil, fmt.Errorf("node: %s backend: %w", c.Backend, err)
	}
	return backend, nil
}

// declared reports whether an operator asked for a container tier.
func (c ContainerConfig) declared() bool { return c.Backend != "" }

// containerTier builds the runtime a ContainerConfig describes. A function, not a
// method: config is inert data that constructs nothing (config_test.go's census).
func containerTier(c ContainerConfig, disks container.Volumes, secrets container.Secrets) (invoke.Runtime, error) {
	backend, err := containerBackend(c)
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
	// empty layer. See node/volume.
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
