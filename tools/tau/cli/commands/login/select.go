package login

import (
	loginI18n "github.com/taubyte/tau/tools/tau/i18n/login"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/urfave/cli/v2"
)

func Select(ctx *cli.Context, name string, setDefault bool) error {
	err := loginLib.Select(ctx, name, setDefault)
	if err != nil {
		return loginI18n.SelectFailed(name, err)
	}
	loginI18n.Selected(name)

	return nil
}
