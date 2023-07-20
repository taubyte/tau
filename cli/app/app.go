package app

import (
	"fmt"

	"github.com/taubyte/odo/cli/node"
	"github.com/urfave/cli/v2"
)

func Run(args ...string) error {
	err := App().Run(args)
	if err != nil {
		return err
	}

	return nil
}

func App() *cli.App {
	app := &cli.App{
		Commands: []*cli.Command{
			startShape(),
		},
	}
	return app
}

func startShape() *cli.Command {
	return &cli.Command{
		Name:        "start",
		Description: "start a shape",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "shape",
				Required: true,
				Aliases:  []string{"s"},
			},
			&cli.PathFlag{
				Name:     "config",
				Required: true,
				Aliases:  []string{"c"},
			},
			&cli.BoolFlag{
				Name: "dev",
			},
		},

		Action: func(ctx *cli.Context) error {
			protocolConfig, sourceConfig, err := parseSourceConfig(ctx)
			if err != nil {
				return fmt.Errorf("parsing config failed with: %s", err)
			}

			setNetworkDomains(sourceConfig)
			return node.Start(ctx.Context, protocolConfig)
		},
	}
}
