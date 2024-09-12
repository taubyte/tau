package loginPrompts

import (
	"fmt"

	flags "github.com/taubyte/tau/tools/tau/flags/login"
	i18n "github.com/taubyte/tau/tools/tau/i18n/login"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slices"
)

func SelectAProvider(ctx *cli.Context) (provider string, err error) {
	if ctx.IsSet(flags.Provider.Name) {
		provider = ctx.String(flags.Provider.Name)
		if slices.Contains(Providers, provider) {
			return
		}
	}

	provider, err = prompts.SelectInterface(Providers, GitProviderPrompt, DefaultProvider)
	if err != nil {
		err = i18n.SelectProviderFailed(err)
		return
	}

	return
}

func GetOrRequireAProviderAndToken(ctx *cli.Context) (provider, token string, err error) {
	if ctx.IsSet(flags.Token.Name) {
		token = ctx.String(flags.Token.Name)
	}

	if ctx.IsSet(flags.Provider.Name) {
		provider = ctx.String(flags.Provider.Name)
	}

	if len(provider) != 0 && len(token) != 0 {
		return
	}

	if len(provider) == 0 {
		provider, err = prompts.SelectInterface(Providers, GitProviderPrompt, DefaultProvider)
		if err != nil {
			err = i18n.SelectProviderFailed(err)
			return
		}
	}

	// Ask to get from web or enter a token
	webOpt := fmt.Sprintf(GetTokenFromWeb, provider)
	selection, err := prompts.SelectInterface(
		[]string{webOpt, EnterTokenManually},
		GetTokenWith,
		webOpt,
	)
	if err != nil {
		err = i18n.SelectTokenFromFailed(err)
		return
	}

	if selection == webOpt {
		token, err = TokenFromWeb(ctx, provider)
	} else {
		token = prompts.GetOrRequireAString(ctx, flags.Token.Name, TokenPrompt, nil) // TODO: validator
	}

	return
}
