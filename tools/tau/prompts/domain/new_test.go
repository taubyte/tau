package domainPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	domainFlags "github.com/taubyte/tau/tools/tau/flags/domain"
	"github.com/taubyte/tau/tools/tau/prompts"
	domainPrompts "github.com/taubyte/tau/tools/tau/prompts/domain"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestNew_AllFlagsSet_GeneratedFQDN_NonInteractive(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedCloud("test")
	prompts.UseDefaults = true
	defer func() { prompts.UseDefaults = false }()

	ctx, err := mock.CLI{
		Flags: flags.Combine(
			flags.Name,
			flags.Description,
			flags.Tags,
			domainFlags.Generated,
			domainFlags.GeneratedPrefix,
			domainFlags.CertType,
		),
		ToSet: map[string]string{
			flags.Name.Name:           "domnew1",
			flags.Description.Name:    "A test domain",
			flags.Tags.Name:           "web",
			domainFlags.CertType.Name: domainFlags.CertTypeAuto,
		},
	}.Run("--name", "domnew1", "--description", "A test domain", "--tags", "web",
		"--generated-fqdn", "--cert-type", domainFlags.CertTypeAuto)
	assert.NilError(t, err)

	dom, err := domainPrompts.New(ctx)
	assert.NilError(t, err)
	assert.Assert(t, dom != nil)
	assert.Equal(t, dom.Name, "domnew1")
	assert.Equal(t, dom.Description, "A test domain")
	assert.Assert(t, dom.Fqdn != "")
	assert.Equal(t, dom.CertType, domainFlags.CertTypeAuto)
}
