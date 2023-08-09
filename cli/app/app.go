package app

import (
	"github.com/urfave/cli/v2"
)

func newApp() *cli.App {
	app := &cli.App{
		Commands: []*cli.Command{
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
