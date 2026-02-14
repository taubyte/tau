package cli

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/cli/commands/autocomplete"
	"github.com/taubyte/tau/tools/tau/cli/commands/current"
	"github.com/taubyte/tau/tools/tau/cli/commands/dream"
	"github.com/taubyte/tau/tools/tau/cli/commands/login"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/application"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/builds"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/builds/build"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/cloud"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/database"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/domain"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/function"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/library"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/logs"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/messaging"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/project"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/service"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/smartops"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/storage"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/website"
	"github.com/taubyte/tau/tools/tau/cli/commands/validate"
	"github.com/taubyte/tau/tools/tau/cli/commands/version"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func New() (*cli.App, error) {
	globalFlags := []cli.Flag{
		flags.Color,
		flags.Defaults,
		flags.Yes,
		flags.Json,
		flags.Toon,
	}

	app := &cli.App{
		UseShortOptionHandling: true,
		Flags:                  globalFlags,
		EnableBashCompletion:   true,
		Before: func(ctx *cli.Context) error {
			prompts.UseDefaults = ctx.Bool(flags.Defaults.Name)
			output.SetFormat(ctx.Bool(flags.Json.Name), ctx.Bool(flags.Toon.Name))

			color, err := flags.GetColor(ctx)
			if err != nil {
				return err
			}

			switch color {
			case flags.ColorNever:
				pterm.DisableColor()
			}

			return nil
		},
		Commands: []*cli.Command{
			login.Command,
			current.Command,
			dream.Command,
			validate.Command,
		},
	}

	common.Attach(app,
		project.New,
		application.New,
		cloud.New,
		database.New,
		domain.New,
		function.New,
		library.New,
		messaging.New,
		service.New,
		smartops.New,
		storage.New,
		website.New,
		builds.New,
		build.New,
		logs.New,
	)

	app.Commands = append(app.Commands, []*cli.Command{
		autocomplete.Command,
		version.Command,
	}...)

	return app, nil
}
