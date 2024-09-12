package login

import (
	loginFlags "github.com/taubyte/tau/tools/tau/flags/login"
	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/prompts"
	loginPrompts "github.com/taubyte/tau/tools/tau/prompts/login"
	"github.com/urfave/cli/v2"
)

func New(ctx *cli.Context, options []string) error {
	name := prompts.GetOrRequireAName(ctx, loginPrompts.ProfileName)

	var setDefault bool
	if len(options) > 0 {
		setDefault = prompts.GetOrAskForBool(ctx, loginFlags.SetDefault.Name, loginPrompts.UseAsDefault)
	} else {
		setDefault = true
	}

	provider, token, err := loginPrompts.GetOrRequireAProviderAndToken(ctx)
	if err != nil {
		return err // Already verbose
	}

	err = loginLib.New(name, provider, token, setDefault)
	if err != nil {
		return loginI18n.CreateFailed(name, err)
	}

	if setDefault {
		loginI18n.CreatedDefault(name)
	} else {
		loginI18n.Created(name)
	}

	return Select(ctx, name, false)
}
