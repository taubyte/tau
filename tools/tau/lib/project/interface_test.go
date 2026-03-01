package projectLib_test

import (
	"testing"

	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestSelectedProjectInterface_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	proj, err := projectLib.SelectedProjectInterface()
	assert.NilError(t, err)
	assert.Assert(t, proj != nil)
	assert.Equal(t, proj.Get().Name(), "TrueTest")
}

func TestSelectedProjectConfig_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	cfg, err := projectLib.SelectedProjectConfig()
	assert.NilError(t, err)
	assert.Equal(t, cfg.Name, "fixture")
	assert.Assert(t, cfg.Location != "")
}

func TestConfirmSelectedProject_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := projectLib.ConfirmSelectedProject()
	assert.NilError(t, err)
}
