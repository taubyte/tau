package project

import (
	"errors"
	"fmt"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	projectPrompts "github.com/taubyte/tau/tools/tau/prompts/project"
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
		if err := session.Unset().SelectedProject(); err != nil {
			return err
		}
		if err := session.Unset().SelectedApplication(); err != nil {
			return err
		}
		projectI18n.ClearedProjectSelection()
		return nil
	}

	project, deselect, err := projectPrompts.GetSelectOrDeselect(ctx)
	if err != nil {
		if errors.Is(err, projectI18n.ErrorNoProjectsFound) {
			pterm.Info.Printf("%s \n  Create new project: %s\n  Import existing project: %s\n", err, pterm.FgGreen.Sprintf("$ tau new project"), pterm.FgGreen.Sprintf("$ tau import project"))
			return nil
		}

		return err
	}

	if deselect {
		err = projectLib.Deselect(ctx, project.Name)
		if err != nil {
			return err
		}
		projectI18n.DeselectedProject(project.Name)
	} else {
		err = projectLib.Select(ctx, project.Name)
		if err != nil {
			return err
		}
		projectI18n.SelectedProject(project.Name)
		checkProjectClonedHelp(project.Name)
	}

	return nil
}
