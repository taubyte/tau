package applicationPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	applicationPrompts "github.com/taubyte/tau/tools/tau/prompts/application"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

// TestGetOrSelect_NameSet_WithTCCFixture verifies GetOrSelect returns the app when
// name flag is set and the TCC fixture env is loaded (so ListResources succeeds).
func TestGetOrSelect_NameSet_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_app1"},
	}.Run("prog", "--name", "test_app1")
	assert.NilError(t, err)

	app, err := applicationPrompts.GetOrSelect(ctx, false)
	assert.NilError(t, err)
	assert.Assert(t, app != nil)
	assert.Equal(t, app.Name, "test_app1")
}

// TestGetOrSelect_NameSet_CaseInsensitive uses fixture and checks match is case-insensitive.
func TestGetOrSelect_NameSet_CaseInsensitive(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "TEST_APP2"},
	}.Run("prog", "--name", "TEST_APP2")
	assert.NilError(t, err)

	app, err := applicationPrompts.GetOrSelect(ctx, false)
	assert.NilError(t, err)
	assert.Assert(t, app != nil)
	assert.Equal(t, app.Name, "test_app2")
}

// TestGetOrSelect_NotFound_WithTCCFixture expects error when name is not in the list.
func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_app"},
	}.Run("prog", "--name", "nonexistent_app")
	assert.NilError(t, err)

	_, err = applicationPrompts.GetOrSelect(ctx, false)
	assert.Assert(t, err != nil)
}
