package prompts_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/i18n/printer"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

var selectionFlag = &cli.StringFlag{Name: "choice"}

func TestGetOrAskForSelection(t *testing.T) {
	t.Run("from_flag", func(t *testing.T) {
		prompts.PromptEnabled = false
		defer func() { prompts.PromptEnabled = true }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{selectionFlag},
			ToSet: map[string]string{selectionFlag.Name: "alpha"},
		}.Run()
		assert.NilError(t, err)

		got, err := prompts.GetOrAskForSelection(ctx, selectionFlag.Name, "Pick one:", []string{"alpha", "beta"})
		assert.NilError(t, err)
		assert.Equal(t, got, "alpha")
	})

	t.Run("case_insensitive", func(t *testing.T) {
		prompts.PromptEnabled = false
		defer func() { prompts.PromptEnabled = true }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{selectionFlag},
			ToSet: map[string]string{selectionFlag.Name: "BeTa"},
		}.Run()
		assert.NilError(t, err)

		got, err := prompts.GetOrAskForSelection(ctx, selectionFlag.Name, "Pick one:", []string{"alpha", "beta"})
		assert.NilError(t, err)
		assert.Equal(t, got, "beta")
	})

	t.Run("invalid_value_warns_and_panics", func(t *testing.T) {
		prompts.PromptEnabled = false
		defer func() { prompts.PromptEnabled = true }()

		var buf bytes.Buffer
		restore := printer.SetOutput(printer.WriterOutput(&buf))
		defer restore()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{selectionFlag},
			ToSet: map[string]string{selectionFlag.Name: "notinlist"},
		}.Run()
		assert.NilError(t, err)

		func() {
			defer func() { recover() }()
			prompts.GetOrAskForSelection(ctx, selectionFlag.Name, "Pick one:", []string{"alpha", "beta"})
		}()

		out := buf.String()
		assert.Assert(t, strings.Contains(out, "not a valid selection"))
		assert.Assert(t, strings.Contains(out, "notinlist"))
	})
}
