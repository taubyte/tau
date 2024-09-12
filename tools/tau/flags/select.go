package flags

import "github.com/urfave/cli/v2"

var Select = &cli.BoolFlag{
	Name:  "select",
	Usage: "Ignore current and trigger a selection prompt",
}
