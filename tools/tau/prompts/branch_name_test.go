package prompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrRequireABranch_FromFlag(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Branch},
		ToSet: map[string]string{flags.Branch.Name: "main"},
	}.Run()
	assert.NilError(t, err)

	got, err := prompts.GetOrRequireABranch(ctx)
	assert.NilError(t, err)
	assert.Equal(t, got, "main")
}

func TestGetOrRequireAName_FromFlagValid(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "my_resource"},
	}.Run()
	assert.NilError(t, err)

	got, err := prompts.GetOrRequireAName(ctx, "Name:", nil...)
	assert.NilError(t, err)
	assert.Equal(t, got, "my_resource")
}

func TestGetOrRequireAUniqueName_FromFlagValid(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "unique_name"},
	}.Run()
	assert.NilError(t, err)

	got, err := prompts.GetOrRequireAUniqueName(ctx, "Name:", []string{"other"}, nil...)
	assert.NilError(t, err)
	assert.Equal(t, got, "unique_name")
}
