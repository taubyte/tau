package flags

import "github.com/urfave/cli/v2"

var Json = &cli.BoolFlag{
	Name:  "json",
	Usage: "Output as JSON instead of table",
}

var Toon = &cli.BoolFlag{
	Name:  "toon",
	Usage: "Output as TOON (Token-Oriented Object Notation) instead of table",
}
