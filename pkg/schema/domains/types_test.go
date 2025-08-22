package domains

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestTypes(t *testing.T) {
	dom := &domain{
		Resource: &basic.Resource{},
		seer:     &seer.Seer{},
		name:     "dom1",
	}

	assert.Equal(t, dom.Name(), "dom1")
	assert.Equal(t, dom.AppName(), "")

	err := dom.WrapError("failed: %s", "test error")
	assert.ErrorContains(t, err, "on domain `dom1`; failed: test error")
}
