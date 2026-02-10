package libraryPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	libraryPrompts "github.com/taubyte/tau/tools/tau/prompts/library"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect_NameSet_Global_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_library1"},
	}.Run("prog", "--name", "test_library1")
	assert.NilError(t, err)

	lib, err := libraryPrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, lib != nil)
	assert.Equal(t, lib.Name, "test_library1")
}

func TestGetOrSelect_NameSet_AppScoped_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_library2"},
	}.Run("prog", "--name", "test_library2")
	assert.NilError(t, err)

	lib, err := libraryPrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, lib != nil)
	assert.Equal(t, lib.Name, "test_library2")
}

func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_library"},
	}.Run("prog", "--name", "nonexistent_library")
	assert.NilError(t, err)

	_, err = libraryPrompts.GetOrSelect(ctx)
	assert.Assert(t, err != nil)
}
