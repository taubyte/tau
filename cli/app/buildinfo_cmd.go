package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/urfave/cli/v2"
)

var (
	debugInfo                 = debug.ReadBuildInfo
	buildInfoOutput io.Writer = os.Stdout
)

func buildInfoCommand() *cli.Command {
	return &cli.Command{
		Name:        "info",
		Description: "build information",
		Subcommands: []*cli.Command{
			{
				Name:        "build",
				Description: "show detailed build information",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name: "json",
					},
					&cli.BoolFlag{
						Name: "deps",
					},
				},
				Action: func(ctx *cli.Context) error {
					info, ok := debugInfo()
					if !ok {
						return errors.New("no build information found")
					}

					if !ctx.Bool("deps") {
						info.Deps = nil
					}

					if ctx.Bool("json") {
						jenc := json.NewEncoder(buildInfoOutput)
						if err := jenc.Encode(info); err != nil {
							return err
						}
					} else {
						fmt.Fprintln(buildInfoOutput, info)
					}

					return nil
				},
			},
			{
				Name:        "commit",
				Description: "show commit",
				Action: func(ctx *cli.Context) error {
					info, ok := debugInfo()
					if !ok {
						return errors.New("no build information found")
					}

					for _, setting := range info.Settings {
						if setting.Key == "vcs.revision" {
							fmt.Fprintln(buildInfoOutput, setting.Value)
							return nil
						}
					}

					return errors.New("no commit information found")
				},
			},
		},
	}
}
