package databaseFlags

import "github.com/urfave/cli/v2"

var Min = &cli.StringFlag{
	Name:  "min",
	Usage: "Minimum replicas to keep",
}
var Max = &cli.StringFlag{
	Name:  "max",
	Usage: "Maximum replicas to keep",
}
