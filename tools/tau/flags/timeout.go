package flags

import "github.com/urfave/cli/v2"

var Timeout = &cli.StringFlag{
	Name:    "timeout",
	Aliases: []string{"ttl"},
	Usage:   "Time to live for an instance",
}
