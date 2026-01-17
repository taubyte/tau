package start

import (
	spec "github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/tools/dream/cli/common"
	"github.com/urfave/cli/v2"
)

func Command(ctx *common.Context) *cli.Command {
	return &cli.Command{
		Name: "start",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "debug",
				Usage: "Output for function calls",
			},
			&cli.StringSliceFlag{
				Name:        "universes",
				Aliases:     []string{"u"},
				Usage:       "List universes separated by comma",
				DefaultText: "A single universe named 'default'",
			},
			&cli.StringFlag{
				Name:        "public",
				DefaultText: "Expose APIs to all interfaces",
			},
			&cli.StringFlag{
				Name:        "branch",
				Usage:       "Set branch",
				DefaultText: spec.DefaultBranches[0],
				Value:       spec.DefaultBranches[0],
				Aliases:     []string{"b"},
			},
		},
		Action: runMultiverse(),
	}
}
