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
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Tags},
		}.Run("--tags", "tag1", "--tags", "tag2")
		assert.NilError(t, err)

		got := prompts.GetOrAskForTags(ctx)
		assert.DeepEqual(t, got, []string{"tag1", "tag2"})
	})

	t.Run("RequiredTags_from_flag", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Tags},
		}.Run("--tags", "a", "--tags", "b")
		assert.NilError(t, err)

		got := prompts.RequiredTags(ctx)
		assert.DeepEqual(t, got, []string{"a", "b"})
	})

	t.Run("GetOrAskForTags_empty_tag_flag", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Tags},
		}.Run("--tags", "")
		assert.NilError(t, err)

		got := prompts.GetOrAskForTags(ctx)
		assert.Assert(t, len(got) == 0, "expected no tags, got %v", got)
		// Must not be a slice containing one empty string (old bug with --tags "")
		assert.Assert(t, !(len(got) == 1 && got[0] == ""), "got []string{\"\"}, expected normalized empty")
	})

	t.Run("RequiredTags_empty_tag_flag_no_hang", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Tags},
		}.Run("--tags", "")
		assert.NilError(t, err)

		got := prompts.RequiredTags(ctx)
		assert.Assert(t, len(got) == 0, "expected empty slice, got %v", got)
	})
}
