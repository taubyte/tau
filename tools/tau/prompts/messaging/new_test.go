package messagingPrompts_test

import (
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	messagingFlags "github.com/taubyte/tau/tools/tau/flags/messaging"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	messagingPrompts "github.com/taubyte/tau/tools/tau/prompts/messaging"
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
			flags.Local,
			flags.MatchRegex,
			flags.Match,
			messagingFlags.MQTT,
			messagingFlags.WebSocket,
		),
		ToSet: map[string]string{
			flags.Name.Name:        "msgnew1",
			flags.Description.Name: "A test messaging",
			flags.Tags.Name:        "tag1",
			flags.Match.Name:       "/ch",
		},
	}.Run("--name", "msgnew1", "--description", "A test messaging", "--tags", "tag1", "--match", "/ch",
		"--local", "--regex", "--mqtt", "--no-web-socket")
	assert.NilError(t, err)

	msg, err := messagingPrompts.New(ctx)
	assert.NilError(t, err)
	assert.Assert(t, msg != nil)
	assert.Equal(t, msg.Name, "msgnew1")
	assert.Equal(t, msg.Description, "A test messaging")
	assert.Equal(t, msg.Match, "/ch")
}

func TestEdit_AllFlagsSet_NonInteractive(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Description,
			flags.Tags,
			flags.Local,
			flags.MatchRegex,
			flags.Match,
			messagingFlags.MQTT,
			messagingFlags.WebSocket,
		),
		ToSet: map[string]string{
			flags.Description.Name: "edited msg",
			flags.Tags.Name:        "t1",
			flags.Match.Name:       "/edited",
		},
	}.Run("--description", "edited msg", "--tags", "t1", "--match", "/edited", "--no-local", "--regex", "--mqtt", "--no-web-socket")
	assert.NilError(t, err)

	prev := &structureSpec.Messaging{
		Name:  "existing",
		Match: "/old",
	}
	err = messagingPrompts.Edit(ctx, prev)
	assert.NilError(t, err)
	assert.Equal(t, prev.Description, "edited msg")
	assert.Equal(t, prev.Match, "/edited")
}
