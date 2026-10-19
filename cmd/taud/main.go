// Command taud runs a v2 node from the file an operator wrote.
//
// It is node/boot and a signal handler. Everything a node is configured with
// lives in that file (see boot.File); the only thing a flag adds is --root,
// because the data directory is where a machine put its disk, which the
// installer knows and the file might not.
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ipfs/go-log/v2"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/node/boot"
)

// Set at release by -ldflags "-X main.version=… -X main.commit=… -X main.date=…".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var logger = log.Logger("tau")

// composed are node options a build adds to every node this binary starts —
// a file built under a tag appends to it in init. The default build adds none.
var composed []node.Option

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// run is main without the exit, so its status is testable.
func run(args []string, stdout, stderr io.Writer) int {
	app := newApp(nil)
	app.Writer = stdout
	app.ErrWriter = stderr
	if err := app.Run(args); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// newApp builds the command line. started, when not nil, is handed the running
// node once it serves, which is how a test learns the addresses it bound.
func newApp(started func(*boot.Running)) *cli.App {
	return &cli.App{
		Name:     "taud",
		Usage:    "run a tau node",
		Commands: []*cli.Command{startCommand(started), configCommand(), versionCommand()},
	}
}

func startCommand(started func(*boot.Running)) *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "start a node",
		Flags: []cli.Flag{
			&cli.PathFlag{Name: "config", Aliases: []string{"c"}, Usage: "the node's config file (`taud config` prints one)", Required: true},
			&cli.PathFlag{Name: "root", Usage: "where the node keeps its data; overrides the file's root"},
		},
		Action: func(ctx *cli.Context) error {
			file, err := boot.Read(ctx.Path("config"))
			if err != nil {
				return err
			}
			if root := ctx.Path("root"); root != "" {
				file.Root = root
			}

			running, err := boot.Start(ctx.Context, file, composed...)
			if err != nil {
				return err
			}
			defer running.Close()

			logger.Info("node running: ", running.Node.Services())
			if address := running.Address(); address != "" {
				logger.Info("gateway on ", address)
			}
			if address := running.SecureAddress(); address != "" {
				logger.Info("gateway (tls) on ", address)
			}
			if started != nil {
				started(running)
			}

			// UNTIL A SIGNAL OR THE CONTEXT ENDS. Returning closes everything
			// that was just started.
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(stop)
			select {
			case <-stop:
			case <-ctx.Context.Done():
			}

			logger.Info("stopping")
			return nil
		},
	}
}

// configCommand prints a config an operator can edit, so what the file looks
// like has an answer that cannot drift from the struct that reads it.
func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "print an example node config",
		Action: func(ctx *cli.Context) error {
			return printExample(ctx.App.Writer)
		},
	}
}

func printExample(w io.Writer) error {
	out, err := yaml.Marshal(boot.File{
		Node: node.Config{
			Services:  node.All,
			Cloud:     "acme",
			Container: node.ContainerConfig{Backend: node.BackendContainerd},
			// Written out although it is the default: :53 needs root or
			// CAP_NET_BIND_SERVICE, and the file should show the number that
			// will fail on a laptop.
			DNSListen: node.DefaultDNSListen,
		},
		Root:      boot.DefaultRoot,
		Listen:    []string{boot.DefaultListen},
		Bootstrap: []string{},
		SwarmKey:  "<base64; a node without one joins the public network>",
		HTTP:      ":8080",
	})
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

func versionCommand() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "print this binary's version",
		Action: func(ctx *cli.Context) error {
			_, err := fmt.Fprintf(ctx.App.Writer, "tau %s (%s, %s)\n", version, commit, date)
			return err
		},
	}
}
