// Command taucorder bridges a tau cloud's libp2p swarm to local clients: it
// joins as a light peer, forwards the v2 services' ConnectRPC on a loopback
// listener, and shows the fleet in a terminal UI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/taubyte/tau/tools/taucorder/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.App(cli.Default()).RunContext(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "taucorder:", err)
		stop()
		os.Exit(1)
	}
}
