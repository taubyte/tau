package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ipfs/go-log/v2"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/services/v2/node"
	"github.com/taubyte/tau/services/v2/node/boot"
)

var logger = log.Logger("tau")

// start_cmd.go boots a node.
//
// THERE IS ONE START AND IT IS NOT CALLED v2. It was, briefly, and that was
// wrong twice over: it implied v1 was the default, and it left two commands for
// one thing so that every operator had to know which of them their cloud was.
// v1 is not a target — a name that says which version you meant is a name that
// says both are supported.

func startCommand() *cli.Command {
	return &cli.Command{
		Name:      "start",
		Usage:     "start a node",
		ArgsUsage: "<config.yaml>",
		Description: "Starts the services this node was told to run, and serves its gateway when it has one.\n" +
			"`tau config` prints a config to edit.",
		Action: func(ctx *cli.Context) error {
			if ctx.NArg() != 1 {
				return fmt.Errorf("name one config file; `tau config` prints an example")
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

			logger.Info("🚀 node running: ", running.Node.Services())
			if file.HTTP != "" && running.Node.Handler() != nil {
				logger.Info("   gateway on ", file.HTTP)
			}

			// UNTIL A SIGNAL OR THE CONTEXT ENDS. Returning here would close
			// everything that was just started.
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

// configCommand prints a config an operator can edit, so the first question —
// what does this file look like — has an answer that cannot drift from the
// struct that reads it.
func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "print an example node config",
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
