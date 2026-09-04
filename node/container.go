package node

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/container"
)

// container.go builds the container tier from what an operator declared.
//
// # Why the tier is configuration and not only an option
//
// A tier is a live object — a connection to a container daemon — so it is
// injected, and WithRuntimes stays the way a composer registers one it already
// holds. But which daemon a MACHINE has is exactly the kind of thing an
// operator writes down, and requiring every caller to build the backend
// themselves means every caller reproduces the same twenty lines and one of
// them gets the isolation wrong.
//
// # The isolation setting is here on purpose
//
// containerd's default runtime handler is runc, and under runc the host kernel
// is the only boundary a container has. A node running code somebody else wrote
// should name a sandboxed handler — `io.containerd.runsc.v1` for gVisor — and
// the field is a handler NAME rather than a flag so that swapping the
// implementation later is configuration rather than a code change.
//
// It is not defaulted to a sandbox, because a handler that is not installed
// fails at container start rather than at boot, and a node that silently could
// not start anything is worse than one that runs what the operator asked for.
// What this does instead is make the choice visible: the field is in the config
// an operator writes, with this doc beside it.

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

// containerBackend is the daemon an operator named.
//
// IT IS SEPARATE FROM THE TIER because two things want it and they must be the
// same one: the tier that runs workloads, and the engine that runs builds. A
// build that auto-detected its own would run on a daemon nobody configured
// while the workloads ran on the one they did.
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

// containerTier builds the runtime a ContainerConfig describes.
//
// IT IS A FUNCTION AND NOT A METHOD ON THE CONFIG, and the difference is the
// rule this whole layer is held to: a config is INERT DATA that survives a
// round trip through yaml and constructs nothing. v1's pkg/config built the
// p2p node, and once a config type owns a live resource every consumer of that
// type has to be handed one — which is how a struct an operator writes becomes
// something a test cannot make without a daemon running.
//
// config_test.go's census holds it: no method on a config type in this package
// returns a resource.
func containerTier(c ContainerConfig) (invoke.Runtime, error) {
	backend, err := containerBackend(c)
	if err != nil {
		return nil, err
	}

	// THE SPEC ALREADY CARRIES THE ARTIFACT. A worker reads the image, the
	// memory limit and the environment off the compiled resource, so there is
	// nothing left for an image resolver to look up — container.ImageFromSpec
	// is that, and the injected ImageFunc stays for a registry mirror or a
	// local override.
	return container.New(container.ImageFromSpec,
		container.WithBackend(backend),
		// THE ONE THING THIS TIER CANNOT DO IS SAID OUT LOUD. A resource's
		// egress allowlist is not enforceable by an nftables floor, so an
		// operator whose author wrote one is told rather than left to assume
		// it took effect.
		container.WithWarn(func(format string, args ...any) { logger.Warnf(format, args...) }),
	)
}
