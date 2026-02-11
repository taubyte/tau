package applicationPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	applicationPrompts "github.com/taubyte/tau/tools/tau/prompts/application"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestNew_AllFlagsSet_NonInteractive(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name, flags.Description, flags.Tags},
		ToSet: map[string]string{
			flags.Name.Name:        "new_app_test",
			flags.Description.Name: "A test app",
			flags.Tags.Name:        "tag1,tag2",
		},
	}.Run("prog", "--name", "new_app_test", "--description", "A test app", "--tags", "tag1", "--tags", "tag2")
	assert.NilError(t, err)

	app, err := applicationPrompts.New(ctx)
	assert.NilError(t, err)
	assert.Assert(t, app != nil)
	assert.Equal(t, app.Name, "new_app_test")
	assert.Equal(t, app.Description, "A test app")
	assert.DeepEqual(t, app.Tags, []string{"tag1", "tag2"})
}
