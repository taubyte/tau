//go:build !ee

package drive

import "gopkg.in/yaml.v3"

// enterpriseSource is the community no-op half of the emit seam: only the ee
// build of spore-drive emits enterprise config (see displace_ee.go). Community
// spore-drive deploys no enterprise block, keeping the invariant that only the
// -tags ee build works with the ee clients.
func (d *sporedrive) enterpriseSource(services []string) map[string]yaml.Node {
	return nil
}
