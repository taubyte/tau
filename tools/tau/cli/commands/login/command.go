package login

import (
	"github.com/taubyte/tau/tools/tau/cli/common/options"
	"github.com/taubyte/tau/tools/tau/flags"
	loginFlags "github.com/taubyte/tau/tools/tau/flags/login"
	"github.com/taubyte/tau/tools/tau/i18n"
	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/prompts"
	loginPrompts "github.com/taubyte/tau/tools/tau/prompts/login"
	slices "github.com/taubyte/utils/slices/string"
	"github.com/urfave/cli/v2"
)

var Command = &cli.Command{
	Name: "login",
	Flags: flags.Combine(
		flags.Name,
		loginFlags.Token,
		loginFlags.Provider,
		loginFlags.New,
		loginFlags.SetDefault,
	),
	ArgsUsage: i18n.ArgsUsageName,
	Action:    Run,
	Before:    options.SetNameAsArgs0,
}

func Run(ctx *cli.Context) error {
	_default, options, err := loginLib.GetProfiles()
	if err != nil {
		return loginI18n.GetProfilesFailed(err)
	}

	// New: if --new or no selectable profiles
	if ctx.Bool(loginFlags.New.Name) || len(options) == 0 {
		return New(ctx, options)
	}

	// Selection
	var name string
	if ctx.IsSet(flags.Name.Name) {
		name = ctx.String(flags.Name.Name)

		if !slices.Contains(options, name) {
			return loginI18n.DoesNotExistIn(name, options)
		}
	} else {
		name, err = prompts.SelectInterface(options, loginPrompts.SelectAProfile, _default)
		if err != nil {
			return err
		}
	}

	return Select(ctx, name, ctx.Bool(loginFlags.SetDefault.Name))
}
