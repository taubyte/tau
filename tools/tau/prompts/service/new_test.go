package servicePrompts_test

import (
	"testing"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/flags"
	serviceFlags "github.com/taubyte/tau/tools/tau/flags/service"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	servicePrompts "github.com/taubyte/tau/tools/tau/prompts/service"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestNew_AllFlagsSet_NonInteractive(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Name,
			flags.Description,
			flags.Tags,
			serviceFlags.Protocol,
		),
		ToSet: map[string]string{
			flags.Name.Name:            "svcnew1",
			flags.Description.Name:     "A test service",
			flags.Tags.Name:            "tag1",
			serviceFlags.Protocol.Name: "p2p",
		},
	}.Run("--name", "svcnew1", "--description", "A test service", "--tags", "tag1", "--protocol", "p2p")
	assert.NilError(t, err)

	svc, err := servicePrompts.New(ctx)
	assert.NilError(t, err)
	assert.Assert(t, svc != nil)
	assert.Equal(t, svc.Name, "svcnew1")
	assert.Equal(t, svc.Description, "A test service")
	assert.Equal(t, svc.Protocol, "p2p")
}

func TestEdit_AllFlagsSet_NonInteractive(t *testing.T) {
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Description,
			flags.Tags,
			serviceFlags.Protocol,
		),
		ToSet: map[string]string{
			flags.Description.Name:     "edited svc",
			flags.Tags.Name:            "t1",
			serviceFlags.Protocol.Name: "/custom/v1",
		},
	}.Run("--description", "edited svc", "--tags", "t1", "--protocol", "/custom/v1")
	assert.NilError(t, err)

	prev := &structureSpec.Service{
		Name:     "existing",
		Protocol: "/old/v1",
	}
	err = servicePrompts.Edit(ctx, prev)
	assert.NilError(t, err)
	assert.Equal(t, prev.Description, "edited svc")
	assert.Equal(t, prev.Protocol, "/custom/v1")
}
