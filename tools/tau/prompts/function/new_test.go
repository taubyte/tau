package functionPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/flags"
	functionFlags "github.com/taubyte/tau/tools/tau/flags/function"
	"github.com/taubyte/tau/tools/tau/prompts"
	functionPrompts "github.com/taubyte/tau/tools/tau/prompts/function"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestNew_P2P_AllFlagsSet_NonInteractive(t *testing.T) {
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
			flags.Local,
			functionFlags.Type,
			functionFlags.P2P(),
		),
		ToSet: map[string]string{
			flags.Name.Name:             "fn_p2p_1",
			flags.Description.Name:      "P2P function",
			flags.Tags.Name:             "tag1",
			flags.Timeout.Name:          "5s",
			flags.Memory.Name:           "10MB",
			flags.Source.Name:           ".",
			flags.Call.Name:             "myCall",
			functionFlags.Type.Name:     common.FunctionTypeP2P,
			functionFlags.Protocol.Name: "test_service1",
			functionFlags.Command.Name:  "mycmd",
		},
	}.Run(
		"--name", "fn_p2p_1", "--description", "P2P function", "--tags", "tag1",
		"--timeout", "5s", "--memory", "10MB", "--source", ".", "--call", "myCall",
		"--type", common.FunctionTypeP2P, "--protocol", "test_service1", "--command", "mycmd",
		"--no-use-template", "--no-local",
	)
	assert.NilError(t, err)

	fn, _, err := functionPrompts.New(ctx)
	assert.NilError(t, err)
	assert.Assert(t, fn != nil)
	assert.Equal(t, fn.Name, "fn_p2p_1")
	assert.Equal(t, fn.Type, common.FunctionTypeP2P)
	assert.Equal(t, fn.Protocol, "test_service1")
	assert.Equal(t, fn.Command, "mycmd")
	assert.Equal(t, fn.Call, "myCall")
}
