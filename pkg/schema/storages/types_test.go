package storages

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestTypes(t *testing.T) {
	stg := &storage{
		Resource: &basic.Resource{},
		seer:     &seer.Seer{},
		name:     "stg1",
	}

	assert.Equal(t, stg.Name(), "stg1")
	assert.Equal(t, stg.AppName(), "")

	err := stg.WrapError("failed: %s", "test error")
	assert.ErrorContains(t, err, "on storage `stg1`; failed: test error")
}
