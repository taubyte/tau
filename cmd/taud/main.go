// Command taud runs a v2 node from the file an operator wrote.
//
// It is instance/boot and a signal handler. Everything a node is configured with
// lives in that file (see boot.File); the only thing a flag adds is --root,
// because the data directory is where a machine put its disk, which the
// installer knows and the file might not.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/taubyte/tau/pkg/containers"

	"github.com/ipfs/go-log/v2"
	"github.com/urfave/cli/v2"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/instance"
	"github.com/taubyte/tau/instance/boot"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/services"
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
var composed []instance.Option

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
		Commands: []*cli.Command{startCommand(started), initCommand(), configCommand(), versionCommand()},
	}
}

func nodeFlags() []cli.Flag {
	return []cli.Flag{
		&cli.PathFlag{Name: "config", Aliases: []string{"c"}, Usage: "the node's config file (`taud config` prints one)", Required: true},
		&cli.PathFlag{Name: "root", Usage: "where the node keeps its data; overrides the file's root"},
	}
}

func readFile(ctx *cli.Context) (boot.File, error) {
	file, err := boot.Read(ctx.Path("config"))
	if err != nil {
		return file, err
	}
	if root := ctx.Path("root"); root != "" {
		file.Root = root
	}
	return file, nil
}

func startCommand(started func(*boot.Running)) *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "start a node",
		Flags: nodeFlags(),
		Action: func(ctx *cli.Context) error {
			file, err := readFile(ctx)
			if err != nil {
				return err
			}

			running, err := boot.Start(ctx.Context, file, composed...)
			if err != nil {
				return err
			}
			defer running.Close()

			logger.Info("node running: ", running.Services())
			if address := running.Address(); address != "" {
				logger.Info("gateway on ", address)
			}
			if address := running.SecureAddress(); address != "" {
				logger.Info("gateway (tls) on ", address)
			}
			if started != nil {
				started(running)
			}

			// Until a signal or the context ends; returning closes everything started.
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

// initCommand writes a sealed bundle of secrets into a stopped node's vault.
// The bundle is read from a file or, as `-`, from stdin, so the secrets need
// never be written to the node's disk in the clear.
func initCommand() *cli.Command {
	return &cli.Command{
		Name:      "init",
		Usage:     "write sealed secrets into this node's vault before it starts",
		ArgsUsage: "<bundle, or - for stdin>",
		Flags:     nodeFlags(),
		Action: func(ctx *cli.Context) error {
			if ctx.NArg() != 1 {
				return errors.New("init takes one bundle: a file, or - for stdin")
			}
			file, err := readFile(ctx)
			if err != nil {
				return err
			}

			var data []byte
			if name := ctx.Args().First(); name == "-" {
				data, err = io.ReadAll(ctx.App.Reader)
			} else {
				data, err = os.ReadFile(name)
			}
			if err != nil {
				return fmt.Errorf("reading the bundle: %w", err)
			}

			var bundle secretsv2.InitBundle
			if err := proto.Unmarshal(data, &bundle); err != nil {
				return fmt.Errorf("reading the bundle: %w", err)
			}
			return boot.Init(ctx.Context, file, &bundle)
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
		Node: instance.Config{
			Services:  append(append([]string(nil), instance.All...), services.Default()...),
			Cloud:     "acme",
			Container: containers.Declared{Backend: containers.Containerd},
			// Written out although it is the default: :53 needs root or
			// CAP_NET_BIND_SERVICE, and the file should show the number that
			// will fail on a laptop.
			DNSListen: instance.DefaultDNSListen,
		},
		Root:        boot.DefaultRoot,
		Listen:      []string{boot.DefaultListen},
		Bootstrap:   []string{},
		PrivateKey:  "<base64; this node's libp2p key, the one its certificate certifies>",
		CA:          "<PEM; the root certificate of the cloud's CA>",
		Certificate: "<PEM; this node's certificate from that CA. Without one the node refuses to start unless open: true>",
		HTTP:        ":8080",
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
