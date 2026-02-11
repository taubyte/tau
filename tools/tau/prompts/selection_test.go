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
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

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
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{selectionFlag},
			ToSet: map[string]string{selectionFlag.Name: "BeTa"},
		}.Run()
		assert.NilError(t, err)

		got, err := prompts.GetOrAskForSelection(ctx, selectionFlag.Name, "Pick one:", []string{"alpha", "beta"})
		assert.NilError(t, err)
		assert.Equal(t, got, "beta")
	})

	t.Run("invalid_value_warns_then_UseDefaults_returns_first", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		var buf bytes.Buffer
		restore := printer.SetOutput(printer.WriterOutput(&buf))
		defer restore()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{selectionFlag},
			ToSet: map[string]string{selectionFlag.Name: "notinlist"},
		}.Run()
		assert.NilError(t, err)

		got, err := prompts.GetOrAskForSelection(ctx, selectionFlag.Name, "Pick one:", []string{"alpha", "beta"})
		assert.NilError(t, err)
		assert.Equal(t, got, "alpha")

		out := buf.String()
		assert.Assert(t, strings.Contains(out, "not a valid selection"))
		assert.Assert(t, strings.Contains(out, "notinlist"))
	})

	t.Run("UseDefaults_uses_first_option_when_no_prev", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{Flags: []cli.Flag{selectionFlag}}.Run()
		assert.NilError(t, err)

		got, err := prompts.GetOrAskForSelection(ctx, selectionFlag.Name, "Pick one:", []string{"alpha", "beta"})
		assert.NilError(t, err)
		assert.Equal(t, got, "alpha")
	})

	t.Run("UseDefaults_uses_prev_when_in_items", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{Flags: []cli.Flag{selectionFlag}}.Run()
		assert.NilError(t, err)

		got, err := prompts.GetOrAskForSelection(ctx, selectionFlag.Name, "Pick one:", []string{"alpha", "beta"}, "beta")
		assert.NilError(t, err)
		assert.Equal(t, got, "beta")
	})

	t.Run("UseDefaults_empty_items_returns_ErrRequiredInDefaultsMode", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{Flags: []cli.Flag{selectionFlag}}.Run()
		assert.NilError(t, err)

		_, err = prompts.GetOrAskForSelection(ctx, selectionFlag.Name, "Pick one:", nil)
		assert.Assert(t, err != nil)
		assert.ErrorIs(t, err, prompts.ErrRequiredInDefaultsMode)
	})
}
