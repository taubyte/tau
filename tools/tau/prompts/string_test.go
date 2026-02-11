package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/validate"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestString(t *testing.T) {
	t.Run("GetOrAskForAStringValue_from_flag", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{&cli.StringFlag{Name: "field"}},
			ToSet: map[string]string{"field": "myvalue"},
		}.Run()
		assert.NilError(t, err)

		got := prompts.GetOrAskForAStringValue(ctx, "field", "Label:", nil...)
		assert.Equal(t, got, "myvalue")
	})

	t.Run("GetOrRequireAString_from_flag", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{flags.Name},
			ToSet: map[string]string{flags.Name.Name: "valid_name"},
		}.Run()
		assert.NilError(t, err)

		got, err := prompts.GetOrRequireAString(ctx, flags.Name.Name, "Name:", validate.VariableNameValidator)
		assert.NilError(t, err)
		assert.Equal(t, got, "valid_name")
	})

	t.Run("GetOrRequireAString_UseDefaults_no_value_returns_ErrRequiredInDefaultsMode", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{Flags: []cli.Flag{flags.Name}}.Run()
		assert.NilError(t, err)

		_, err = prompts.GetOrRequireAString(ctx, flags.Name.Name, "Name:", validate.VariableNameValidator)
		assert.Assert(t, err != nil)
		assert.ErrorIs(t, err, prompts.ErrRequiredInDefaultsMode)
	})
}
