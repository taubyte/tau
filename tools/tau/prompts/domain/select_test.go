package domainPrompts_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/flags"
	domainPrompts "github.com/taubyte/tau/tools/tau/prompts/domain"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrSelect_NameSet_Global_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_domain1"},
	}.Run("prog", "--name", "test_domain1")
	assert.NilError(t, err)

	dom, err := domainPrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, dom != nil)
	assert.Equal(t, dom.Name, "test_domain1")
}

func TestGetOrSelect_NameSet_AppScoped_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "test_domain2"},
	}.Run("prog", "--name", "test_domain2")
	assert.NilError(t, err)

	dom, err := domainPrompts.GetOrSelect(ctx)
	assert.NilError(t, err)
	assert.Assert(t, dom != nil)
	assert.Equal(t, dom.Name, "test_domain2")
}

func TestGetOrSelect_NotFound_WithTCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)

	ctx, err := mock.CLI{
		Flags: []cli.Flag{flags.Name},
		ToSet: map[string]string{flags.Name.Name: "nonexistent_domain"},
	}.Run("prog", "--name", "nonexistent_domain")
	assert.NilError(t, err)

	_, err = domainPrompts.GetOrSelect(ctx)
	assert.Assert(t, err != nil)
}
