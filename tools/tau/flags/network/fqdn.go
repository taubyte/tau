package network

import "github.com/urfave/cli/v2"

var FQDN = &cli.StringFlag{
	Name:    "fqdn",
	Aliases: []string{"f"},
	Usage:   "FQDN of remote network to connect to",
}
