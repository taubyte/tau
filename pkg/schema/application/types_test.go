package application

import (
	"testing"

	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
	"gotest.tools/v3/assert"
)

func TestTypes(t *testing.T) {
	app := &application{
		Resource: &basic.Resource{},
		seer:     &seer.Seer{},
		name:     "app1",
	}

	assert.Equal(t, app.Name(), "app1")
	assert.Equal(t, app.AppName(), "")

	err := app.WrapError("failed: %s", "test error")
	assert.ErrorContains(t, err, "on application `app1`; failed: test error")
}
