package smartopsPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	smartopsPrompts "github.com/taubyte/tau/tools/tau/prompts/smartops"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestNew_AllFlagsSet_NonInteractive(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Name,
			flags.Description,
			flags.Tags,
			flags.Timeout,
			flags.Memory,
			flags.MemoryUnit,
			flags.Source,
			flags.Call,
			flags.Template,
			flags.UseCodeTemplate,
		),
		ToSet: map[string]string{
			flags.Name.Name:        "smartnew1",
			flags.Description.Name: "A test smartop",
			flags.Tags.Name:        "tag1",
			flags.Timeout.Name:     "5s",
			flags.Memory.Name:      "10MB",
			flags.Source.Name:      ".",
			flags.Call.Name:        "myCall",
		},
	}.Run("--name", "smartnew1", "--description", "A test smartop", "--tags", "tag1",
		"--timeout", "5s", "--memory", "10MB", "--source", ".", "--call", "myCall", "--no-use-template")
	assert.NilError(t, err)

	op, _, err := smartopsPrompts.New(ctx)
	assert.NilError(t, err)
	assert.Assert(t, op != nil)
	assert.Equal(t, op.Name, "smartnew1")
	assert.Equal(t, op.Description, "A test smartop")
	assert.Equal(t, op.Call, "myCall")
}
