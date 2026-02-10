package websitePrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	websitePrompts "github.com/taubyte/tau/tools/tau/prompts/website"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect_NameSet_Global_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_website1"},
	}.Run("prog", "--name", "test_website1")
	assert.NilError(t, err)

	ws, err := websitePrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, ws != nil)
	assert.Equal(t, ws.Name, "test_website1")
}

func TestGetOrSelect_NameSet_AppScoped_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_website2"},
	}.Run("prog", "--name", "test_website2")
	assert.NilError(t, err)

	ws, err := websitePrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, ws != nil)
	assert.Equal(t, ws.Name, "test_website2")
}

func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_website"},
	}.Run("prog", "--name", "nonexistent_website")
	assert.NilError(t, err)

	_, err = websitePrompts.GetOrSelect(ctx)
	assert.Assert(t, err != nil)
}
