package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

var methodFlag = &cli.StringFlag{Name: "method"}

func TestSelectInterface(t *testing.T) {
	t.Run("empty_options_returns_error", func(t *testing.T) {
		_, err := prompts.SelectInterface(nil, "Pick:", "")
		assert.Assert(t, err != nil)
	})

	t.Run("UseDefaults_returns_first_option", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		got, err := prompts.SelectInterface([]string{"a", "b", "c"}, "Pick:", "")
		assert.NilError(t, err)
		assert.Equal(t, got, "a")
	})

	t.Run("UseDefaults_returns_default_when_in_names", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		got, err := prompts.SelectInterface([]string{"a", "b", "c"}, "Pick:", "b")
		assert.NilError(t, err)
		assert.Equal(t, got, "b")
	})
}

func TestSelectInterfaceField(t *testing.T) {
	t.Run("from_flag", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{methodFlag},
			ToSet: map[string]string{methodFlag.Name: "get"},
		}.Run()
		assert.NilError(t, err)

		got, err := prompts.SelectInterfaceField(ctx, []string{"get", "post"}, methodFlag.Name, "Method:", nil...)
		assert.NilError(t, err)
		assert.Equal(t, got, "get")
	})

	t.Run("case_insensitive", func(t *testing.T) {
		prompts.UseDefaults = true
		defer func() { prompts.UseDefaults = false }()

		ctx, err := mock.CLI{
			Flags: []cli.Flag{methodFlag},
			ToSet: map[string]string{methodFlag.Name: "POST"},
		}.Run()
		assert.NilError(t, err)

		got, err := prompts.SelectInterfaceField(ctx, []string{"get", "post"}, methodFlag.Name, "Method:", nil...)
		assert.NilError(t, err)
		assert.Equal(t, got, "post")
	})
}
