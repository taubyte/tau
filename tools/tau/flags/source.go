package flags

import "github.com/urfave/cli/v2"

var Source = &cli.StringFlag{
	Name:  "source",
	Usage: "Path within the code folder or a library repository ex: . | libraries/<library>",
}
