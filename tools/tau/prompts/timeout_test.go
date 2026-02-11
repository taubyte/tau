package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrRequireATimeout_FromFlag(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Timeout},
		ToSet: map[string]string{flags.Timeout.Name: "5s"},
	}.Run("--timeout", "5s")
	assert.NilError(t, err)

	timeout, err := prompts.GetOrRequireATimeout(ctx)
	assert.NilError(t, err)
	assert.Equal(t, timeout, uint64(5_000_000_000))
}
