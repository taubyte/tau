package website

import (
	"testing"

	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
	"gotest.tools/v3/assert"
)

func TestTypes(t *testing.T) {
	web := &website{
		Resource: &basic.Resource{},
		seer:     &seer.Seer{},
		name:     "web1",
	}

	assert.Equal(t, web.Name(), "web1")
	assert.Equal(t, web.AppName(), "")

	err := web.WrapError("failed: %s", "test error")
	assert.ErrorContains(t, err, "on website `web1`; failed: test error")
}
