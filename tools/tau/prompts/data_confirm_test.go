package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestConfirmData(t *testing.T) {
	t.Run("with_yes_flag", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{Flags: []cli.Flag{flags.Yes}}.Run("--y")
		assert.NilError(t, err)
		assert.Assert(t, prompts.ConfirmData(ctx, "Confirm?", [][]string{{"a", "1"}, {"b", "2"}}))
	})
}

func TestConfirmDataWithMerge(t *testing.T) {
	t.Run("with_yes_flag", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{Flags: []cli.Flag{flags.Yes}}.Run("-y")
		assert.NilError(t, err)
		assert.Assert(t, prompts.ConfirmDataWithMerge(ctx, "Confirm?", [][]string{{"k", "v"}}))
	})
}
