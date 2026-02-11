package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetSizeAndType_New_FromFlag(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Size, flags.SizeUnit},
		ToSet: map[string]string{flags.Size.Name: "10GB"},
	}.Run("--size", "10GB")
	assert.NilError(t, err)

	size, err := prompts.GetSizeAndType(ctx, "", true)
	assert.NilError(t, err)
	assert.Equal(t, size, "10GB")
}
