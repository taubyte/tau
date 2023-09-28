package app

import (
	"github.com/urfave/cli/v2"
)

// newApp creates a new instance of the CLI application
func newApp() *cli.App {
	app := &cli.App{
		Commands: []*cli.Command{ // Defining the commands for the application
			startCommand(), // Adding the start command
			configCommand(), // Adding the config command
		},
	}
	return app
}

// Run is the entry point of the application
func Run(args ...string) error {
	err := newApp().Run(args) // Running the CLI application with the provided arguments
	if err != nil {
		return err
	}

	return nil
}
