package dream

import (
	"fmt"

	"github.com/taubyte/tau/tools/tau/cli/commands/dream/build"
	dreamLib "github.com/taubyte/tau/tools/tau/lib/dream"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/urfave/cli/v2"
)

const (
	defaultBind        = "node@1/verbose,seer@2/copies,node@2/copies"
	dreamCacheLocation = "~/.cache/dreamland/universe-tau"
)

var (
	cacheDream = []string{"--id", "tau", "--keep"}
)

var Command = &cli.Command{
	Name:  "dream",
	Usage: "Starts and interfaces with a local taubyte network.  All leading arguments to `tau dream ...` are passed to dreamland",
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:  "cache",
			Usage: fmt.Sprintf("caches the universe in `%s` keeping data for subsequent restarts", dreamCacheLocation),
		},
	},
	Action: func(c *cli.Context) error {
		project, err := projectLib.SelectedProjectInterface()
		if err != nil {
			return err
		}

		h := projectLib.Repository(project.Get().Name())
		projectRepositories, err := h.Open()
		if err != nil {
			return err
		}

		branch, err := projectRepositories.CurrentBranch()
		if err != nil {
			return err
		}

		baseStartDream := []string{"new", "multiverse", "--bind", defaultBind, "--branch", branch}
		if c.IsSet("cache") {
			return dreamLib.Execute(append(baseStartDream, cacheDream...)...)
		} else {
			return dreamLib.Execute(baseStartDream...)
		}
	},

	Subcommands: []*cli.Command{
		injectCommand,
		attachCommand,
		build.Command,
	},
}
