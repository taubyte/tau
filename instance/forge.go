package instance

import (
	"errors"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/drivers"
)

// WithForges replaces the configured forges with a registry built elsewhere (an
// in-memory forge in a test).
func WithForges(r *forge.Registry) Option {
	return func(o *options) error {
		if r == nil {
			return errors.New("node: a nil forge registry")
		}
		o.forges = r
		return nil
	}
}

// forgeRegistry is the injected registry, or the one the config declares.
func (n *Instance) forgeRegistry() (*forge.Registry, error) {
	if n.opts.forges != nil {
		return n.opts.forges, nil
	}
	oauth := make([]string, 0, len(n.cfg.Auth.Forges))
	for _, app := range n.cfg.Auth.Forges {
		oauth = append(oauth, app.Host)
	}
	return drivers.Registry(n.cfg.Forges, oauth...)
}
