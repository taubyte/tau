package satellite

import "github.com/hashicorp/go-plugin"

func New(name string, exports func() map[string]interface{}) plugin.Plugin {
	return &satellite{
		name:    name,
		exports: exports(),
	}
}
