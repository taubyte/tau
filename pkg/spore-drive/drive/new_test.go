package drive

import (
	"testing"

	"github.com/taubyte/tau/pkg/spore-drive/config/fixtures"
	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	_, p := fixtures.VirtConfig()
	_, err := New(p)
	assert.NilError(t, err)
}
