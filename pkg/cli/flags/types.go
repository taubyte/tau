package flags

import "github.com/urfave/cli/v2"

type BoolWithInverse interface {
	Value(ctx *cli.Context) bool
	IsSet(ctx *cli.Context) bool
}
