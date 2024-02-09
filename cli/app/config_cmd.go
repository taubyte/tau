package app

import (
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
						Name:    "path",
						Aliases: []string{"p"},
					},
				},
				Action: func(ctx *cli.Context) error {
					_, _, _, err := parseSourceConfig(ctx, ctx.String("shape"))
					return err
				},
			},
			{
				Name:    "show",
				Aliases: []string{"render", "display", "print"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "shape",
						Aliases: []string{"s"},
					},
					&cli.PathFlag{
						Name:    "path",
						Aliases: []string{"p"},
					},
				},
				Action: func(ctx *cli.Context) error {
					pid, cnf, _, err := parseSourceConfig(ctx, ctx.String("shape"))
					if err != nil {
						return err
					}
					return displayConfig(pid, cnf)
				},
			},
			{
				Name:  "export",
				Usage: "export a configuration bundle",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "unsafe",
						Usage: "export node private key (Only use to restore a node).",
					},
					&cli.StringFlag{
						Name:    "shape",
						Aliases: []string{"s"},
					},
					&cli.BoolFlag{
						Name:    "protect",
						Aliases: []string{"p"},
					},
				},
				Action: exportConfig,
			},
			{
				Name:    "generate",
				Aliases: []string{"gen"},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "shape",
						Aliases: []string{"s"},
					},
					&cli.StringFlag{
						Name:    "protocols",
						Aliases: []string{"proto", "protos"},
						Usage:   "Protocols to enable. Use `all` to enable them all.",
					},
					&cli.StringFlag{
						Name:    "network",
						Aliases: []string{"n", "fqdn"},
						Value:   "example.com",
					},
					&cli.IntFlag{
						Name:    "p2p-port",
						Aliases: []string{"port", "p2p"},
						Value:   4242,
					},
					&cli.StringSliceFlag{
						Name:    "ip",
						Aliases: []string{"announce"},
						Usage:   "IP address to announce.",
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
					&cli.PathFlag{
						Name:  "use",
						Usage: "use a configuration template",
					},
				},
				Action: generateSourceConfig,
			},
		},
	}
}
