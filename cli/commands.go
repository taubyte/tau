package cli

import (
	"fmt"

	"bitbucket.org/taubyte/odo/node"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	"github.com/urfave/cli/v2"
)

func Build() (*cli.App, error) {
	app := &cli.App{
		Commands: []*cli.Command{},
	}

	app.Commands = append(app.Commands, startShape())

	return app, nil
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
		},

		Action: func(ctx *cli.Context) error {
			shape := ctx.String("shape")
			var config commonIface.GenericConfig
			_, err := config.Parse(ctx)
			if err != nil {
				return fmt.Errorf("parsing ctx for shape `%s` failed with: %s", shape, err)
			}

			return node.Start(ctx.Context, &config, shape)
		},
	}
}
