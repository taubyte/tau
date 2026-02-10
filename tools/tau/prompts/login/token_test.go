package loginPrompts

import (
	"testing"

	loginFlags "github.com/taubyte/tau/tools/tau/flags/login"
	"github.com/taubyte/tau/tools/tau/prompts/internal/mock"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestGetOrRequireAProviderAndToken_FlagsSet_NonInteractive(t *testing.T) {
	ctx, err := mock.CLI{
		Flags: []cli.Flag{loginFlags.Provider, loginFlags.Token},
		ToSet: map[string]string{
			loginFlags.Provider.Name: "github",
			loginFlags.Token.Name:    "test-token-123",
		},
	}.Run("prog", "--provider", "github", "--token", "test-token-123")
	assert.NilError(t, err)

	provider, token, err := GetOrRequireAProviderAndToken(ctx)
	assert.NilError(t, err)
	assert.Equal(t, provider, "github")
	assert.Equal(t, token, "test-token-123")
}
