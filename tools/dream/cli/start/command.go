package start

import (
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func Command(ctx *common.Context) *cli.Command {
	return &cli.Command{
		Name:      "start",
		Usage:     "Start Dream and keep it running until Ctrl+C",
		ArgsUsage: "[multiverse]",
		Category:  "Start & stop",
		Description: `Starts the Dream API and MCP server on ` + common.DefaultDreamURL() + `, brings back the
universes kept from the last run, then starts the ones named with --universes.
Stops cleanly on Ctrl+C. The optional argument names the multiverse (default: "default").

Examples:
   dream start
   dream start --universes blackhole
   dream start --fresh --universes blackhole,test`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "Log every function call to a file in the temp directory",
			},
			&cli.StringSliceFlag{
				Name:        "universes",
				Aliases:     []string{"u"},
				Usage:       "Universes to start with every service, comma separated",
				DefaultText: "none; kept universes come back by themselves",
			},
			&cli.BoolFlag{
				Name:  "fresh",
				Usage: "Delete the kept universes and start empty",
			},
			&cli.StringFlag{
				Name:        "public",
				Usage:       "true: expose the APIs on all interfaces (0.0.0.0)",
				DefaultText: "127.0.0.1 only",
			},
			&cli.StringFlag{
				Name:        "branch",
				Usage:       "Git branch the services build from",
				DefaultText: spec.DefaultBranches[0],
				Value:       spec.DefaultBranches[0],
				Aliases:     []string{"b"},
			},
		},
		Action: runMultiverse(),
	}
}
