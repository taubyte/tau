package cloud

import "github.com/urfave/cli/v2"

var FQDN = &cli.StringFlag{
	Name:    "fqdn",
	Aliases: []string{"f"},
	Usage:   "FQDN of remote cloud to connect to",
}
