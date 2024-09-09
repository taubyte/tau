package serviceFlags

import "github.com/urfave/cli/v2"

var Protocol = &cli.StringFlag{
	Name:    "protocol",
	Aliases: []string{"p"},
	Usage:   "Protocol to use for service discovery",
}
