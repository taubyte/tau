package smartops

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestTypes(t *testing.T) {
	smart := &smartOps{
		Resource: &basic.Resource{},
		seer:     &seer.Seer{},
		name:     "smart1",
	}

	assert.Equal(t, smart.Name(), "smart1")
	assert.Equal(t, smart.AppName(), "")

	err := smart.WrapError("failed: %s", "test error")
	assert.ErrorContains(t, err, "on smartops `smart1`; failed: test error")
}
