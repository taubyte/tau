package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Relative
	"github.com/pterm/pterm"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/tools/dream/cli/common"
	inject "github.com/taubyte/tau/tools/dream/cli/inject"
	"github.com/taubyte/tau/tools/dream/cli/kill"
	"github.com/taubyte/tau/tools/dream/cli/new"
	"github.com/taubyte/tau/tools/dream/cli/reset"
	"github.com/taubyte/tau/tools/dream/cli/start"
	"github.com/taubyte/tau/tools/dream/cli/status"

	// Actual imports
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/urfave/cli/v2"

	// Empty imports for initializing fixtures, and client/service run methods"
	_ "github.com/taubyte/tau/clients/p2p/auth/dream"
	_ "github.com/taubyte/tau/clients/p2p/hoarder/dream"
	_ "github.com/taubyte/tau/clients/p2p/monkey/dream"
	_ "github.com/taubyte/tau/clients/p2p/patrick/dream"
	_ "github.com/taubyte/tau/clients/p2p/seer/dream"
	_ "github.com/taubyte/tau/clients/p2p/substrate/dream"
	_ "github.com/taubyte/tau/clients/p2p/tns/dream"
	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/gateway/dream"
	_ "github.com/taubyte/tau/services/hoarder/dream"
	_ "github.com/taubyte/tau/services/monkey/dream"
	_ "github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/patrick/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
	_ "github.com/taubyte/tau/services/substrate/dream"
	_ "github.com/taubyte/tau/services/tns/dream"
)

func main() {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-signals
		switch sig {
		case os.Interrupt, syscall.SIGTERM:
			fmt.Println()
			pterm.Info.Println("Received signal... Shutting down.")
			ctxC()
		}
	}()

	multiverse, err := client.New(
		ctx,
		client.URL(common.DefaultDreamURL()),
		// Give time for fixtures to execute
		// We should maybe use WebSocket later
		client.Timeout(300*time.Second),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not create the Dream client: %s\n", err)
		os.Exit(1)
	}

	err = defineCLI(&common.Context{Ctx: ctx, Multiverse: multiverse}).RunContext(ctx, os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", common.Friendly(err, universeNames(multiverse)))
		os.Exit(1)
	}
}

// universeNames lists the running universes, for the "universe not found" hint.
func universeNames(m *client.Client) func() ([]string, error) {
	return func() ([]string, error) {
		info, err := m.Universes()
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(info))
		for name := range info {
			names = append(names, name)
		}
		return names, nil
	}
}

func init() {
	// prints "dream <version> (commit <short>)", the line tau doctor reads
	cli.VersionPrinter = func(c *cli.Context) { fmt.Fprintln(c.App.Writer, c.App.Version) }
}

func defineCLI(ctx *common.Context) *(cli.App) {
	app := &cli.App{
		Name:      "dream",
		Usage:     "run a local Taubyte cloud for development",
		UsageText: "dream <command> [subcommand] [flags] [arguments]",
		Description: `Dream runs a whole Taubyte cloud on this machine: universes made of nodes
(auth, tns, patrick, monkey, substrate, gateway...). Pair it with the tau CLI to
build and test projects without a remote cloud. The API listens on ` + common.DefaultDreamURL() + `.

Put flags BEFORE the name: dream new universe --bind gateway@18080/http blackhole

Examples:
   dream start                                             start Dream; kept universes come back
   dream new universe --bind gateway@18080/http blackhole  add a universe, gateway on port 18080
   dream status universe blackhole                         list its nodes and ports
   dream inject push-all --force true blackhole            rebuild every repo
   dream kill universe blackhole                           stop that universe`,
		CommandNotFound:        common.CommandNotFound,
		UseShortOptionHandling: true,
		Commands: []*cli.Command{
			new.Command(ctx),
			start.Command(ctx),
			inject.Command(ctx),
			kill.Command(ctx),
			status.Command(ctx),
			reset.Command(ctx),
			{
				Name:     "version",
				Usage:    "Print the Dream version and commit",
				Category: "Inspect",
				Action:   func(c *cli.Context) error { cli.ShowVersion(c); return nil },
			},
		},
		Version:              dream.BuildVersion().String(),
		Suggest:              true,
		EnableBashCompletion: true,
	}

	return app
}
