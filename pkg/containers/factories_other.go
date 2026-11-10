//go:build !linux

package containers

import (
	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
)

// carried is every backend this build can run: containerd is Linux's alone.
var carried = factories{
	core.BackendTypeDocker: docker.Factory,
}
