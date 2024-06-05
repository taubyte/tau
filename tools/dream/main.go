package main

import (
	"context"
	"log"
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
	"github.com/taubyte/tau/tools/dream/cli/status"

	// Actual imports
	client "github.com/taubyte/tau/clients/http/dream"
	"github.com/urfave/cli/v2"

	// Empty imports for initializing fixtures, and client/service run methods"
	_ "github.com/taubyte/tau/clients/p2p/auth"
	_ "github.com/taubyte/tau/clients/p2p/hoarder"
	_ "github.com/taubyte/tau/clients/p2p/monkey"
	_ "github.com/taubyte/tau/clients/p2p/patrick"
	_ "github.com/taubyte/tau/clients/p2p/seer"
	_ "github.com/taubyte/tau/clients/p2p/tns"
	_ "github.com/taubyte/tau/dream/fixtures"
	_ "github.com/taubyte/tau/services/auth"
	_ "github.com/taubyte/tau/services/gateway"
	_ "github.com/taubyte/tau/services/hoarder"
	_ "github.com/taubyte/tau/services/monkey"
	_ "github.com/taubyte/tau/services/monkey/fixtures/compile"
	_ "github.com/taubyte/tau/services/patrick"
	_ "github.com/taubyte/tau/services/seer"
	_ "github.com/taubyte/tau/services/substrate"
	_ "github.com/taubyte/tau/services/tns"
)

func main() {
	ctx, ctxC := context.WithCancel(context.Background())

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-signals
		switch sig {
		case os.Interrupt, syscall.SIGTERM:
			pterm.Info.Println("Received signal... Shutting down.")
			ctxC()
		}
	}()

	defer func() {
		if common.DoDaemon {
			ctxC()
			dream.Zeno()
		}
	}()

	multiverse, err := client.New(
		ctx,
		client.URL(common.DefaultDreamlandURL),
		// Give time for fixtures to execute
		// We should maybe use WebSocket later
		client.Timeout(300*time.Second),
	)
	if err != nil {
		log.Fatalf("Starting new dreamland client failed with: %s", err.Error())
	}

	err = defineCLI(&common.Context{Ctx: ctx, Multiverse: multiverse}).RunContext(ctx, os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func defineCLI(ctx *common.Context) *(cli.App) {
	app := &cli.App{
		UseShortOptionHandling: true,
		Commands: []*cli.Command{
			new.Command(ctx),
			inject.Command(ctx),
			kill.Command(ctx),
			status.Command(ctx),
		},
		Suggest:              true,
		EnableBashCompletion: true,
	}

	return app
}
