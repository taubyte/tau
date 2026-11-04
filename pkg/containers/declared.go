package containers

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/containers/core"
)

// Which container daemon a machine has is configuration; the backend over it is a
// live object, opened from what an operator declared.
//
// containerd's default handler is runc, where the host kernel is the only boundary.
// A node running others' code should name a sandboxed handler
// (`io.containerd.runsc.v1` for gVisor). Not defaulted: an uninstalled handler fails
// at container start, not boot.

// The backends a Declared may name.
const (
	Docker     = "docker"
	Containerd = "containerd"
)

// Declared is the container daemon an operator declared for a machine.
type Declared struct {
	// Backend is "docker", "containerd", or empty for none.
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

// ErrUnknownBackend is a backend name nothing here can open.
var ErrUnknownBackend = errors.New("containers: not a container backend this node can open")

// Any reports whether an operator declared a daemon at all.
func (d Declared) Any() bool { return d.Backend != "" }

// Open is the daemon d declares. A function and not a method: a declaration is inert
// data that constructs nothing.
func Open(d Declared) (core.Backend, error) {
	var cfg core.BackendConfig

	switch d.Backend {
	case Docker:
		cfg = core.DockerConfig{Host: d.Socket}
	case Containerd:
		cfg = core.ContainerdConfig{
			SocketPath: d.Socket,
			Namespace:  d.Namespace,
			Runtime:    d.Runtime,
		}
	default:
		return nil, fmt.Errorf("%w: %q; it opens %s or %s", ErrUnknownBackend, d.Backend, Docker, Containerd)
	}

	backend, err := NewBackend(cfg)
	if err != nil {
		return nil, fmt.Errorf("containers: %s backend: %w", d.Backend, err)
	}
	return backend, nil
}
