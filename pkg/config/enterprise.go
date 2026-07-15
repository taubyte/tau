package config

import "gopkg.in/yaml.v3"

// EnterpriseConfig returns the raw enterprise.<service> config node when present.
// tau/spore-drive treat the enterprise config opaquely; an ee service decodes its
// own block from this node, so no service schema lives in the public tree.
func EnterpriseConfig(c Config, service string) (yaml.Node, bool) {
	cc, ok := c.(*config)
	if !ok {
		return yaml.Node{}, false
	}
	n, ok := cc.enterprise[service]
	return n, ok
}
