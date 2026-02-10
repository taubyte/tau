package messagingPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	messagingPrompts "github.com/taubyte/tau/tools/tau/prompts/messaging"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect_NameSet_Global_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_messaging1"},
	}.Run("prog", "--name", "test_messaging1")
	assert.NilError(t, err)

	m, err := messagingPrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, m != nil)
	assert.Equal(t, m.Name, "test_messaging1")
}

func TestGetOrSelect_NameSet_AppScoped_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_messaging2"},
	}.Run("prog", "--name", "test_messaging2")
	assert.NilError(t, err)

	m, err := messagingPrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, m != nil)
	assert.Equal(t, m.Name, "test_messaging2")
}

func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_messaging"},
	}.Run("prog", "--name", "nonexistent_messaging")
	assert.NilError(t, err)

	_, err = messagingPrompts.GetOrSelect(ctx)
	assert.Assert(t, err != nil)
}
