package config_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/session"
	"gotest.tools/v3/assert"
)

func TestGetSelectedApplication_FromSession(t *testing.T) {
	session.Clear()
	defer session.Clear()

	dir := t.TempDir()
	err := session.LoadSessionInDir(dir)
	assert.NilError(t, err)

	err = session.Set().SelectedApplication("myapp")
	assert.NilError(t, err)

	app, ok := config.GetSelectedApplication()
	assert.Assert(t, ok)
	assert.Equal(t, app, "myapp")
}
