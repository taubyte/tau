//go:build ee

package drive

import (
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/spore-drive/config"
)

// enterpriseSource emits the enterprise config for services present in the
// shape (ee build only; community returns nil — see displace_noee.go). Gating
// by shape membership mirrors the accounts emit: a shape only gets the
// enterprise blocks for services it actually runs.
func (d *sporedrive) enterpriseSource(services []string) map[string]yaml.Node {
	all := config.EnterpriseServices(d.parser)
	if len(all) == 0 {
		return nil
	}

	out := make(map[string]yaml.Node)
	for _, svc := range services {
		if node, ok := all[svc]; ok {
			out[svc] = node
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
