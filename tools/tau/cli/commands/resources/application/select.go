package application

import (
	"fmt"

	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	applicationI18n "github.com/taubyte/tau/tools/tau/i18n/application"
	applicationLib "github.com/taubyte/tau/tools/tau/lib/application"
	applicationPrompts "github.com/taubyte/tau/tools/tau/prompts/application"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/urfave/cli/v2"
)

func (link) Select() common.Command {
	return common.Create(
		&cli.Command{
			Flags:  []cli.Flag{flags.None},
			Action: _select,
		},
	)
}

func _select(ctx *cli.Context) error {
	if ctx.IsSet(flags.Name.Name) && ctx.Bool(flags.None.Name) {
		return fmt.Errorf("cannot use --name and --none together")
	}
	if ctx.Bool(flags.None.Name) {
		if err := session.Unset().SelectedApplication(); err != nil {
			return err
		}
		applicationI18n.ClearedApplicationSelection()
		return nil
	}

	application, deselect, err := applicationPrompts.GetSelectOrDeselect(ctx)
	if err != nil {
		return err
	}

	if deselect {
		err = applicationLib.Deselect(ctx, application.Name)
		if err != nil {
			return err
		}
		applicationI18n.Deselected(application.Name)
	} else {
		err = applicationLib.Select(ctx, application.Name)
		if err != nil {
			return err
		}
		applicationI18n.Selected(application.Name)
	}

	return nil
}
