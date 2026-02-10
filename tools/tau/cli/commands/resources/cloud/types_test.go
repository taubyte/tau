package cloud_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/cli/commands/resources/cloud"
	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	basic := cloud.New()
	assert.Assert(t, basic != nil)
	cmd := basic.New()
	assert.Assert(t, cmd == nil)
}
