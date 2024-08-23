package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

func newApp() *cli.App {
	app := &cli.App{
		Name:  "spin",
		Usage: "WebAssembly Sanboxed container runtime",
		Commands: []*cli.Command{
			pullCommand,
			runCommand,
		},
	}
	return app
}

func main() {
	err := newApp().Run(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
