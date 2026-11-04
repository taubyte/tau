package instance

import (
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/drivers"
)

// forgeRegistry is the forges the config declares, bound to their drivers. A gitlab
// auth has an OAuth app for is one people sign in to with an OAuth token.
func (n *Instance) forgeRegistry() (*forge.Registry, error) {
	oauth := make([]string, 0, len(n.cfg.Auth.Forges))
	for _, app := range n.cfg.Auth.Forges {
		oauth = append(oauth, app.Host)
	}
	return drivers.Registry(n.cfg.Forges, oauth...)
}
