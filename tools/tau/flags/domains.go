package flags

import "github.com/urfave/cli/v2"

var Domains = &cli.StringSliceFlag{
	Name:  "domains",
	Usage: "List of domains (comma, separated) by name or FQDN",
}
