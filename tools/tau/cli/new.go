package cli

import (
	"github.com/pterm/pterm"
	accountsCmd "github.com/taubyte/tau/tools/tau/cli/commands/accounts"
	"github.com/taubyte/tau/tools/tau/cli/commands/autocomplete"
	buildCmd "github.com/taubyte/tau/tools/tau/cli/commands/build"
	"github.com/taubyte/tau/tools/tau/cli/commands/current"
	"github.com/taubyte/tau/tools/tau/cli/commands/login"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/builds"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/builds/build"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/cloud"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/generic"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/logs"
	"github.com/taubyte/tau/tools/tau/cli/commands/resources/project"
	"github.com/taubyte/tau/tools/tau/cli/commands/validate"
	"github.com/taubyte/tau/tools/tau/cli/commands/version"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	"github.com/taubyte/tau/tools/tau/output"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/taubyte/tau/tools/tau/tcc"
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
			buildCmd.Command,
			validate.Command,
			accountsCmd.Command,
		},
	}

	// Resource commands come from the tcc DSL: one command set per resource
	// kind it defines, built from its schema.
	resources, err := resourceCommands()
	if err != nil {
		return nil, err
	}

	common.Attach(app, append([]common.BasicFunction{
		project.New,
		cloud.New,
		builds.New,
		build.New,
		logs.New,
	}, resources...)...)

	app.Commands = append(app.Commands, []*cli.Command{
		autocomplete.Command,
		version.Command,
	}...)

	return app, nil
}

// resourceCommands binds one command set per resource kind the DSL defines.
func resourceCommands() ([]common.BasicFunction, error) {
	groups, err := tcc.Groups()
	if err != nil {
		return nil, err
	}

	out := make([]common.BasicFunction, 0, len(groups))
	for _, g := range groups {
		cmd, err := generic.New(g)
		if err != nil {
			return nil, err
		}
		out = append(out, cmd)
	}

	return out, nil
}
