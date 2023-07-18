package main

import "github.com/urfave/cli/v2"

var dev = []cli.Flag{
	&cli.BoolFlag{
		Name: "dev",
	},
}
