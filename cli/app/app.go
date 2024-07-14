package app

import (
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
)

func newApp() *cli.App {
	app := &cli.App{
		Flags: []cli.Flag{
			&cli.PathFlag{
				Name:  "root",
				Value: config.DefaultRoot,
				Usage: "Folder where tau is installed",
			},
		},
		Commands: []*cli.Command{
			buildInfoCommand(),
			startCommand(),
			configCommand(),
		},
	}
	return app
}

func Run(args ...string) error {
	err := newApp().Run(args)
	if err != nil {
		return err
	}

	return nil
}
