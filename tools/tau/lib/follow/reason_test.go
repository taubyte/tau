package follow

import (
	"testing"

	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

// A job that says why it failed is reported with that, not its log's last line.
func TestLogTailPrefersTheJobsReason(t *testing.T) {
	got, _ := logTail(nil, &patrick.Job{Reason: "the build ran out of memory"})
	assert.Equal(t, got, "the build ran out of memory")
}
