package jobs

import (
	"testing"

	"gotest.tools/v3/assert"
)

// Every job on a node shares one build limit, on real clouds too: a job's ops
// build in parallel up to it instead of one at a time.
func TestJobsShareNodeBuildSlots(t *testing.T) {
	a, b := Context{}.buildSlots(), Context{}.buildSlots()
	assert.Equal(t, a, b, "jobs must share the node's slots")
	assert.Equal(t, cap(a), cap(nodeBuildSlots))
	assert.Assert(t, cap(a) >= 1)
}
