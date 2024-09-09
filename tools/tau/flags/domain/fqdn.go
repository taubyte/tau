package domainFlags

import "github.com/urfave/cli/v2"

var FQDN = &cli.StringFlag{
	Name:    "fqdn",
	Aliases: []string{"f"},
	Usage:   "Fully-qualified domain name",
}
