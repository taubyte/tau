package servicePrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	servicePrompts "github.com/taubyte/tau/tools/tau/prompts/service"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect_NameSet_Global_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_service1"},
	}.Run("prog", "--name", "test_service1")
	assert.NilError(t, err)

	svc, err := servicePrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	assert.Equal(t, svc.Name, "test_service1")
}

func TestGetOrSelect_NameSet_AppScoped_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_service2"},
	}.Run("prog", "--name", "test_service2")
	assert.NilError(t, err)

	svc, err := servicePrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	assert.Equal(t, svc.Name, "test_service2")
}

func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_service"},
	}.Run("prog", "--name", "nonexistent_service")
	assert.NilError(t, err)

	_, err = servicePrompts.GetOrSelect(ctx)
	assert.Assert(t, err != nil)
}
