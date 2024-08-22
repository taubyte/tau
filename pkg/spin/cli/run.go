package main

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/taubyte/tau/pkg/spin"
	"github.com/taubyte/tau/pkg/spin/registry"

	"github.com/urfave/cli/v2"

	//lint:ignore ST1001 ignore
	. "github.com/taubyte/tau/pkg/spin/runtime"
)

func root() (path string) {
	// Get the current user's profile
	usr, err := user.Current()
	if err != nil {
		panic(fmt.Errorf("error fetching user profile: %w", err))
	}

	// Join the home directory with the desired path
	path = filepath.Join(usr.HomeDir, ".spin")
	return
}

var runCommand = &cli.Command{
	Name:  "run",
	Usage: "runs a container",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "arch",
			Aliases: []string{"a"},
		},
		&cli.StringFlag{
			Name:    "module",
			Aliases: []string{"m"},
		},
	},
	Action: func(ctx *cli.Context) error {
		reg, err := registry.New(ctx.Context, root())
		if err != nil {
			return fmt.Errorf("registry init failed with %w", err)
		}

		var opts []spin.Option[spin.Spin]
		switch ctx.String("arch") {
		case "amd64":
			opts = append(opts, Runtime[AMD64](reg))
		case "riscv64":
			opts = append(opts, Runtime[AMD64](reg))
		}

		isRuntime := true
		if ctx.String("module") != "" {
			opts = append(opts, ModuleOpen(ctx.String("module")))
			isRuntime = false
		}

		s, err := New(ctx.Context, opts...)
		if err != nil {
			return fmt.Errorf("runtime init failed with %w", err)
		}
		defer s.Close()

		containerOpts := []spin.Option[spin.Container]{
			Stdin(os.Stdin),
			Stdout(os.Stdout),
			Stderr(os.Stderr),
		}
		if isRuntime {
			image := ctx.Args().First()
			if image == "" {
				return errors.New("you need to provide image name")
			}
			containerOpts = append(containerOpts, Image(image), Command(ctx.Args().Slice()[1:]...))
		} else {
			containerOpts = append(containerOpts, Command(ctx.Args().Slice()...))
		}

		c, err := s.New(containerOpts...)
		if err != nil {
			return fmt.Errorf("container creation failed with %w", err)
		}
		defer c.Stop()

		err = c.Run()
		if err != nil {
			return fmt.Errorf("running container failed with %w", err)
		}

		return nil
	},
}
