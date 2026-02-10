package smartopsPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	smartopsPrompts "github.com/taubyte/tau/tools/tau/prompts/smartops"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect_NameSet_Global_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_smartops1"},
	}.Run("prog", "--name", "test_smartops1")
	assert.NilError(t, err)

	op, err := smartopsPrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, op != nil)
	assert.Equal(t, op.Name, "test_smartops1")
}

func TestGetOrSelect_NameSet_AppScoped_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_smartops2"},
	}.Run("prog", "--name", "test_smartops2")
	assert.NilError(t, err)

	op, err := smartopsPrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, op != nil)
	assert.Equal(t, op.Name, "test_smartops2")
}

func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_smartops"},
	}.Run("prog", "--name", "nonexistent_smartops")
	assert.NilError(t, err)

	_, err = smartopsPrompts.GetOrSelect(ctx)
	assert.Assert(t, err != nil)
}
