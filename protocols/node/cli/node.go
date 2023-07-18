package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	logging "github.com/ipfs/go-log/v2"
	p2p "github.com/taubyte/go-interfaces/p2p/peer"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	"github.com/taubyte/odo/protocols/node/service"
	"github.com/urfave/cli/v2"
)

var (
	Logger        = logging.Logger("node")
	Node          p2p.Node
	Context       context.Context
	ContextCancel context.CancelFunc
)

func initLogger() {
	lvl, _ := logging.LevelFromString("ERROR")
	logging.SetAllLoggers(lvl)
}

func initContext() {
	Context, ContextCancel = context.WithCancel(context.Background())

	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		ContextCancel()
	}()
}

func nodeDone() {
	//https://github.com/ipfs/go-ipfs/blob/8f623c9124d6c0b1d511a072a4d13633884c7b40/core/builder.go
	<-Node.Done()
	Node.Close()

	os.Exit(0)
}

func StartNode(c *cli.Context) {
	config := &commonIface.GenericConfig{}
	if c.IsSet("dev") {
		config.DevMode = true
	}

	initLogger()
	initContext()

	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		fmt.Println("Exiting...")
		ContextCancel()
	}()

	srv, err := service.New(Context, config)
	if err != nil {
		Logger.Error(err.Error())
		fmt.Println(err)
		Context.Done()
		os.Exit(0)
	}

	Node = srv.Node()

}
