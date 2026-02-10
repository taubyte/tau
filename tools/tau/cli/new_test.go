package cli

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	app, err := New()
	assert.NilError(t, err)
	assert.Assert(t, app != nil)
	assert.Assert(t, len(app.Commands) > 0)
}

func TestRun_SingleArg(t *testing.T) {
	// Run with single arg runs app (e.g. shows help or runs default)
	err := Run("tau")
	assert.NilError(t, err)
}
