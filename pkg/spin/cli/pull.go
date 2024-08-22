package main

import (
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/spin/registry"
	"github.com/urfave/cli/v2"
)

var pullCommand = &cli.Command{
	Name:  "pull",
	Usage: "pulls and converts container images",
	Action: func(ctx *cli.Context) error {
		reg, err := registry.New(ctx.Context, root())
		if err != nil {
			return fmt.Errorf("registry init failed with %w", err)
		}

		image := ctx.Args().First()
		if image == "" {
			return errors.New("you need to provide image name")
		}

		return reg.Pull(ctx.Context, image)
	},
}
