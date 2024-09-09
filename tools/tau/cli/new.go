package cli

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/cli/commands/autocomplete"
	"github.com/taubyte/tau/tools/tau/cli/commands/current"
	"github.com/taubyte/tau/tools/tau/cli/commands/dream"
	"github.com/taubyte/tau/tools/tau/cli/commands/exit"
	"github.com/taubyte/tau/tools/tau/cli/commands/login"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/application"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/builds"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/builds/build"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/database"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/domain"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/function"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/library"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/logs"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/messaging"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/network"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/project"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/service"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/smartops"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/storage"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/website"
	"github.com/taubyte/tau/tools/tau/cli/commands/version"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/states"
	"github.com/urfave/cli/v2"
)

func New() (*cli.App, error) {
	globalFlags := []cli.Flag{
		flags.Env,
		flags.Color,
	}

	app := &cli.App{
		UseShortOptionHandling: true,
		Flags:                  globalFlags,
		EnableBashCompletion:   true,
		Before: func(ctx *cli.Context) error {
			states.New(ctx.Context)

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
			exit.Command,
			dream.Command,
		},
	}

	common.Attach(app,
		project.New,
		application.New,
		network.New,
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
