package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestConfirmPrompt(t *testing.T) {
	t.Run("with_yes_flag", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{Flags: []cli.Flag{flags.Yes}}.Run("--yes")
		assert.NilError(t, err)
		assert.Assert(t, prompts.ConfirmPrompt(ctx, "Confirm?"))
	})

	t.Run("UseDefaults_returns_false_without_yes", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{Flags: []cli.Flag{flags.Yes}}.Run()
		assert.NilError(t, err)
		assert.Assert(t, !prompts.ConfirmPrompt(ctx, "Confirm?"))
	})
}
