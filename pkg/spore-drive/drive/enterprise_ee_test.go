//go:build ee

package drive

import (
	"testing"

	"github.com/taubyte/tau/pkg/spore-drive/config"
	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	"gotest.tools/v3/assert"
)

// TestEnterpriseEmit exercises the generic enterprise config path: spore-drive
// stores/emits an opaque per-service config; it knows no service's schema. The
// service-specific round-trip lives in the ee submodule.
func TestEnterpriseEmit(t *testing.T) {
	_, p := fixtures.VirtConfig()

	assert.NilError(t, config.SetEnterprisePath(p, "svc", []string{"top"}, "v1"))
	assert.NilError(t, config.SetEnterprisePath(p, "svc", []string{"nested", "leaf"}, "v2"))
	assert.NilError(t, config.SetEnterprisePath(p, "svc", []string{"list"}, []string{"a", "b"}))

	sd, err := New(p)
	assert.NilError(t, err)
	d := sd.(*sporedrive)

	src := d.enterpriseSource([]string{"svc", "seer"})
	node, ok := src["svc"]
	assert.Assert(t, ok, "enterprise block not emitted for a service in the shape")

	var got struct {
		Top    string `yaml:"top"`
		Nested struct {
			Leaf string `yaml:"leaf"`
		} `yaml:"nested"`
		List []string `yaml:"list"`
	}
	assert.NilError(t, node.Decode(&got))
	assert.Equal(t, got.Top, "v1")
	assert.Equal(t, got.Nested.Leaf, "v2")
	assert.Equal(t, len(got.List), 2)

	// a shape without the service emits nothing
	assert.Assert(t, d.enterpriseSource([]string{"seer"}) == nil)
}
