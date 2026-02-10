package storagePrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	storagePrompts "github.com/taubyte/tau/tools/tau/prompts/storage"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect_NameSet_Global_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_storage1"},
	}.Run("prog", "--name", "test_storage1")
	assert.NilError(t, err)

	s, err := storagePrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, s != nil)
	assert.Equal(t, s.Name, "test_storage1")
}

func TestGetOrSelect_NameSet_AppScoped_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_storage2"},
	}.Run("prog", "--name", "test_storage2")
	assert.NilError(t, err)

	s, err := storagePrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, s != nil)
	assert.Equal(t, s.Name, "test_storage2")
}

func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_storage"},
	}.Run("prog", "--name", "nonexistent_storage")
	assert.NilError(t, err)

	_, err = storagePrompts.GetOrSelect(ctx)
	assert.Assert(t, err != nil)
}
