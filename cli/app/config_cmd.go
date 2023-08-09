package app

import (
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/config"
	"github.com/urfave/cli/v2"
)

func configCommand() *cli.Command {
	return &cli.Command{
		Name:        "config",
		Aliases:     []string{"cnf", "conf"},
		Description: "configuration utils",
		Subcommands: []*cli.Command{
			{
				Name:    "validate",
				Aliases: []string{"check", "ok", "ok?", "valid?"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "shape",
						Aliases: []string{"s"},
					},
					&cli.PathFlag{
						Name:        "root",
						DefaultText: config.DefaultRoot,
					},
					&cli.PathFlag{
						Name:    "path",
						Aliases: []string{"p"},
					},
				},
				Action: func(ctx *cli.Context) error {
					_, _, err := parseSourceConfig(ctx)
					return err
				},
			},
			{
				Name:    "generate",
				Aliases: []string{"gen"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "shape",
						Aliases: []string{"s"},
					},
					&cli.PathFlag{
						Name:  "root",
						Value: config.DefaultRoot,
					},
					&cli.StringFlag{
						Name:    "protocols",
						Aliases: []string{"proto", "protos"},
					},
					&cli.StringFlag{
						Name:    "network",
						Aliases: []string{"fqdn"},
						Value:   "example.com",
					},
					&cli.IntFlag{
						Name:    "p2p-port",
						Aliases: []string{"port", "p2p"},
						Value:   4242,
					},
					&cli.StringSliceFlag{
						Name:    "ip",
						Aliases: []string{"address", "addr"},
					},
					&cli.StringSliceFlag{
						Name: "bootstrap",
					},
					&cli.BoolFlag{
						Name:    "swarm-key",
						Aliases: []string{"swarm"},
					},
					&cli.BoolFlag{
						Name:    "dv-keys",
						Aliases: []string{"dv"},
					},
				},
				Action: func(ctx *cli.Context) error {
					id, err := generateSourceConfig(ctx)
					if id != "" {
						pterm.Info.Println("ID:", id)
					}
					return err
				},
			},
		},
	}
}
