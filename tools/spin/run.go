package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/spin"
	"github.com/taubyte/tau/pkg/spin/registry"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"

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
			Name: "zip",
		},
		&cli.StringFlag{
			Name:    "module",
			Aliases: []string{"m"},
		},
		&cli.StringSliceFlag{
			Name:    "mount",
			Aliases: []string{"v"},
		},
		&cli.StringSliceFlag{
			Name:    "env",
			Aliases: []string{"e"},
		},
		&cli.BoolFlag{
			Name:    "network",
			Aliases: []string{"net"},
		},
		&cli.StringSliceFlag{
			Name:    "port",
			Aliases: []string{"p"},
		},
		&cli.BoolFlag{
			Name: "no-stdin",
		},
	},
	Action: func(ctx *cli.Context) error {
		cCtx, cCtxC := context.WithCancel(ctx.Context)
		defer cCtxC()

		pbar := mpb.New(mpb.WithWidth(60), mpb.WithRefreshRate(100*time.Millisecond), mpb.WithOutput(os.Stderr))

		reg, err := registry.New(cCtx, root())
		if err != nil {
			return fmt.Errorf("registry init failed with %w", err)
		}

		var opts []spin.Option[spin.Spin]
		switch ctx.String("arch") {
		case "amd64":
			opts = append(opts, Runtime[AMD64](reg))
		case "riscv64":
			opts = append(opts, Runtime[RISCV64](reg))
		}

		isRuntime := true
		if ctx.String("module") != "" {
			if ctx.String("zip") != "" {
				opts = append(opts, ModuleZip(ctx.String("zip"), ctx.String("module")))
			} else {
				opts = append(opts, ModuleOpen(ctx.String("module")))
			}
			isRuntime = false
		}

		containerOpts := []spin.Option[spin.Container]{
			Stdout(os.Stdout),
			Stderr(os.Stderr),
		}

		if !ctx.Bool("no-stdin") {
			containerOpts = append(containerOpts, Stdin(os.Stdin))
		}

		for _, ev := range ctx.StringSlice("env") {
			splt := strings.Split(ev, "=")
			if len(splt) != 2 {
				return fmt.Errorf("invalid environment entry: %s", ev)
			}
			containerOpts = append(containerOpts, Env(splt[0], splt[1]))
		}

		for _, mn := range ctx.StringSlice("mount") {
			splt := strings.Split(mn, ":")
			if len(splt) != 2 {
				return fmt.Errorf("invalid mount entry: %s", mn)
			}
			containerOpts = append(containerOpts, Mount(splt[0], splt[1]))
		}

		if ctx.Bool("network") {
			var netOpt []spin.Option[*NetworkConfig]
			for _, mn := range ctx.StringSlice("port") {
				splt := strings.Split(mn, ":")
				var hport, gport string
				switch len(splt) {
				case 2:
					hport, gport = splt[0], splt[1]
				case 3:
					hport, gport = splt[0]+":"+splt[1], splt[2]
				default:
					return fmt.Errorf("invalid port mapping: %s", mn)
				}
				netOpt = append(netOpt, Forward(hport, gport))
			}
			containerOpts = append(containerOpts, Networking(netOpt...))
		}

		pullChan := make(chan error, 1)

		if isRuntime {
			image := ctx.Args().First()
			if image == "" {
				return errors.New("you need to provide image name")
			}
			containerOpts = append(containerOpts, Image(image), Command(ctx.Args().Slice()[1:]...))
			// start pulling
			pull(cCtx, reg, image, pbar, pullChan, true)
		} else {
			containerOpts = append(containerOpts, Command(ctx.Args().Slice()...))
			pullChan <- nil
		}

		startupBarName := "Loading runtime"
		if !isRuntime {
			startupBarName = "Loading module"
		}
		startupBar, _ := pbar.Add(1,
			mpb.SpinnerStyle("∙∙∙", "●∙∙", "∙●∙", "∙∙●", "∙∙∙").Build(),
			mpb.BarRemoveOnComplete(),
			mpb.BarFillerTrim(),
			mpb.PrependDecorators(
				decor.Name(startupBarName),
			),
		)
		s, err := New(cCtx, opts...)
		if err != nil {
			return fmt.Errorf("runtime init failed with %w", err)
		}
		defer s.Close()
		startupBar.Increment()

		err = <-pullChan
		if err != nil {
			return fmt.Errorf("pulling image failed with %w", err)
		}

		pbar.Wait()

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
