package dream

import (
	dreamI18n "github.com/taubyte/tau/tools/tau/i18n/dream"
	dreamLib "github.com/taubyte/tau/tools/tau/lib/dream"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/urfave/cli/v2"
)

var injectCommand = &cli.Command{
	Name: "inject",
	Subcommands: []*cli.Command{
		{
			Name: "project",
			Action: func(ctx *cli.Context) error {
				if !dreamLib.IsRunning() {
					dreamI18n.Help().IsDreamlandRunning()
					return dreamI18n.ErrorDreamlandNotStarted
				}

				project, err := projectLib.SelectedProjectInterface()
				if err != nil {
					return err
				}

				profile, err := loginLib.GetSelectedProfile()
				if err != nil {
					return err
				}

				prodProject := &dreamLib.ProdProject{
					Project: project,
					Profile: profile,
				}

				return prodProject.Import()
			},
		},
	},
}
