package instance

import (
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/drivers"
)

// forgeRegistry is the forges the config declares, bound to their drivers.
func (n *Instance) forgeRegistry() (*forge.Registry, error) {
	return drivers.Registry(n.cfg.Forges)
}
