package app

import (
	"fmt"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/cli/node"
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
)

var logger = log.Logger("tau")

func startCommand() *cli.Command {
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
				Name:  "root",
				Value: config.DefaultRoot,
			},
			&cli.BoolFlag{
				Name:    "dev-mode",
				Aliases: []string{"dev"},
			},
		},

		Action: func(ctx *cli.Context) error {
			shape := ctx.String("shape")
			_, serviceConfig, _, err := parseSourceConfig(ctx, shape)
			if err != nil {
				return fmt.Errorf("parsing config failed with: %s", err)
			}

			logger.Info("🚀 Starting Tau (shape: ", shape, ")")

			return node.Start(ctx.Context, serviceConfig)
		},
	}
}
