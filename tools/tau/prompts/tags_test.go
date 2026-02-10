package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestTags(t *testing.T) {
	t.Run("GetOrAskForTags_FromFlag", func(t *testing.T) {
		prompts.PromptEnabled = false
		defer func() { prompts.PromptEnabled = true }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Tags},
		}.Run("--tags", "tag1", "--tags", "tag2")
		assert.NilError(t, err)

		got := prompts.GetOrAskForTags(ctx)
		assert.DeepEqual(t, got, []string{"tag1", "tag2"})
	})

	t.Run("RequiredTags_from_flag", func(t *testing.T) {
		prompts.PromptEnabled = false
		defer func() { prompts.PromptEnabled = true }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Tags},
		}.Run("--tags", "a", "--tags", "b")
		assert.NilError(t, err)

		got := prompts.RequiredTags(ctx)
		assert.DeepEqual(t, got, []string{"a", "b"})
	})
}
