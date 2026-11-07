package jobs

import (
	"testing"

	"gotest.tools/v3/assert"
)

// Identical sources in two projects are two builds; the same project reuses.
func TestReuseIsPerProject(t *testing.T) {
	assert.Assert(t, reuseKey("projectA", "", "src") != reuseKey("projectB", "", "src"))
	assert.Assert(t, reuseKey("projectA", "website", "src") != reuseKey("projectB", "website", "src"))
	assert.Assert(t, reuseKey("projectA", "website", "src") != reuseKey("projectA", "library", "src"))
	assert.Equal(t, reuseKey("projectA", "", "src"), reuseKey("projectA", "", "src"))
}
