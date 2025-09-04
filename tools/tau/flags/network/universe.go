package network

import "github.com/urfave/cli/v2"

var Universe = &cli.StringFlag{
	Name:    "universe",
	Aliases: []string{"u"},
	Usage:   "Dream universe to connect to",
}
