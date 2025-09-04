package services

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
	"gotest.tools/v3/assert"
)

func TestTypes(t *testing.T) {
	srv := &service{
		Resource: &basic.Resource{},
		seer:     &seer.Seer{},
		name:     "srv1",
	}

	assert.Equal(t, srv.Name(), "srv1")
	assert.Equal(t, srv.AppName(), "")

	err := srv.WrapError("failed: %s", "test error")
	assert.ErrorContains(t, err, "on service `srv1`; failed: test error")
}
