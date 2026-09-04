package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/services/v2/node"
	"github.com/taubyte/tau/services/v2/node/boot"
)

// v2_cmd.go is how a v2 node is started, and it is deliberately its own command
// rather than a flag on `start`.
//
// `tau start` boots a v1 shape from a v1 config, and the two vocabularies do not
// map onto each other: a flag would have to decide what every v1 setting means
// to a v2 node, which is a mapping nobody could check. Two commands, two files,
// and nothing silently reinterpreted.

func v2Command() *cli.Command {
	return &cli.Command{
		Name:        "v2",
		Usage:       "run a v2 node",
		Description: "Starts the v2 services this node was told to run, and serves its gateway when it has one.",
		Subcommands: []*cli.Command{
			v2StartCommand(),
			v2ConfigCommand(),
		},
	}
}

func v2StartCommand() *cli.Command {
	return &cli.Command{
		Name:      "start",
		Usage:     "start a v2 node from a config file",
		ArgsUsage: "<config.yaml>",
		Action: func(ctx *cli.Context) error {
			if ctx.NArg() != 1 {
				return fmt.Errorf("name one config file; `tau v2 config` prints an example")
			}

			file, err := boot.Read(ctx.Args().First())
			if err != nil {
				return err
			}

			running, err := boot.Start(ctx.Context, file)
			if err != nil {
				return err
			}
			defer running.Close()

			logger.Info("🚀 v2 node running: ", running.Node.Services())
			if addr := file.HTTP; addr != "" && running.Node.Handler() != nil {
				logger.Info("   gateway on ", addr)
			}

			// UNTIL A SIGNAL OR THE CONTEXT ENDS. A node that returned here
			// would close everything it just started.
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
			select {
			case <-stop:
			case <-ctx.Context.Done():
			}

			logger.Info("stopping")
			return nil
		},
	}
}

// v2ConfigCommand prints a config an operator can edit, so the first question —
// what does this file look like — has an answer that cannot drift from the
// struct that reads it.
func v2ConfigCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "print an example v2 node config",
		Action: func(ctx *cli.Context) error {
			example := boot.File{
				Node: node.Config{
					Services:  node.All,
					Cloud:     "acme",
					Container: node.ContainerConfig{Backend: node.BackendContainerd},
				},
				Root:      boot.DefaultRoot,
				Listen:    []string{boot.DefaultListen},
				Bootstrap: []string{},
				SwarmKey:  "<base64; a node without one joins the public network>",
				HTTP:      ":8080",
			}

			out, err := yaml.Marshal(example)
			if err != nil {
				return err
			}
			fmt.Fprint(ctx.App.Writer, string(out))
			return nil
		},
	}
}
