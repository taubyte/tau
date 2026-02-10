package databasePrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	databasePrompts "github.com/taubyte/tau/tools/tau/prompts/database"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect_NameSet_Global_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_database1"},
	}.Run("prog", "--name", "test_database1")
	assert.NilError(t, err)

	db, err := databasePrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, db != nil)
	assert.Equal(t, db.Name, "test_database1")
}

func TestGetOrSelect_NameSet_AppScoped_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_database2"},
	}.Run("prog", "--name", "test_database2")
	assert.NilError(t, err)

	db, err := databasePrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, db != nil)
	assert.Equal(t, db.Name, "test_database2")
}

func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_db"},
	}.Run("prog", "--name", "nonexistent_db")
	assert.NilError(t, err)

	_, err = databasePrompts.GetOrSelect(ctx)
	assert.Assert(t, err != nil)
}
