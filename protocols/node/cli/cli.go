package main

import (
	"fmt"
	"time"

	"github.com/urfave/cli/v2"
	"golang.org/x/net/context"
)

func defineCLI() *(cli.App) {
	return &cli.App{
		Commands: []*cli.Command{
			{
				Name:  "version",
				Usage: "Print version",
				Action: func(c *cli.Context) error {
					fmt.Println("Taubyte Node version 0.1")
					return nil
				},
			},
			{
				Name:  "start",
				Usage: "Start node",
				Action: func(c *cli.Context) error {
					StartNode(c)
					return nil
				},
				Flags: dev,
			},
			{
				Name:    "utils",
				Aliases: []string{"u"},
				Usage:   "P2P utils",
				Subcommands: []*cli.Command{
					{
						Name:  "provide",
						Usage: "Provide files to the network",
						Action: func(c *cli.Context) error {
							return UtilsProvide(c, c.Args().Slice())
						},
						Flags: dev,
					},
					{
						Name:  "fetch",
						Usage: "Fetch cids from the network",
						Action: func(c *cli.Context) error {
							ctx, ctx_cancel := context.WithTimeout(c.Context, 10*time.Second)
							defer ctx_cancel()
							return UtilsFetch(c, ctx, c.Args().Get(0))
						},
						Flags: dev,
					},
					{
						Name:  "genkey",
						Usage: "Generate Private key",
						Action: func(c *cli.Context) error {
							return UtilsGenKey()
						},
						Flags: dev,
					},
					{
						Name:    "ping",
						Aliases: []string{"p"},
						Usage:   "ping a node providing ID",
						Action: func(c *cli.Context) error {
							return UtilsPing(c, c.Args().First())
						},
						Flags: dev,
					},
					{
						Name:    "addr",
						Aliases: []string{"a"},
						Usage:   "Show local addresses",
						Flags:   dev,
					},
					{
						Name:  "peers",
						Usage: "Show peers",
						Action: func(c *cli.Context) error {
							return UtilsPeers(c)
						},
						Flags: dev,
					},
				},
			},
		},
	}
}
