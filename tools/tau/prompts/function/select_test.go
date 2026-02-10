package functionPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect(t *testing.T) {
	t.Run("global_by_name", func(t *testing.T) {
		testutil.WithTCCFixtureEnv(t)

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Name},
			ToSet: map[string]string{flags.Name.Name: "test_function1_glob"},
		}.Run("prog", "--name", "test_function1_glob")
		assert.NilError(t, err)

		fn, err := functionPrompts.GetOrSelect(ctx)
		assert.NilError(t, err)
		assert.Assert(t, fn != nil)
		assert.Equal(t, fn.Name, "test_function1_glob")
	})

	t.Run("app_scoped_by_name", func(t *testing.T) {
		testutil.WithTCCFixtureEnv(t)
		session.Set().SelectedApplication("test_app1")

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Name},
			ToSet: map[string]string{flags.Name.Name: "test_function2"},
		}.Run("prog", "--name", "test_function2")
		assert.NilError(t, err)

		fn, err := functionPrompts.GetOrSelect(ctx)
		assert.NilError(t, err)
		assert.Assert(t, fn != nil)
		assert.Equal(t, fn.Name, "test_function2")
	})

	t.Run("not_found", func(t *testing.T) {
		testutil.WithTCCFixtureEnv(t)

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Name},
			ToSet: map[string]string{flags.Name.Name: "nonexistent_function"},
		}.Run("prog", "--name", "nonexistent_function")
		assert.NilError(t, err)

		_, err = functionPrompts.GetOrSelect(ctx)
		assert.Assert(t, err != nil)
	})
}
