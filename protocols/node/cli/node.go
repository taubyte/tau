package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	moody "bitbucket.org/taubyte/go-moody-blues"
	bbMoodyCommon "bitbucket.org/taubyte/go-moody-blues/common"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	p2p "github.com/taubyte/go-interfaces/p2p/peer"
	odoConfig "github.com/taubyte/odo/config"
	"github.com/taubyte/odo/protocols/node/service"
	"github.com/urfave/cli/v2"
)

var (
	Logger, _     = moody.New("node")
	Node          p2p.Node
	Context       context.Context
	ContextCancel context.CancelFunc
)

func initLogger() {
	bbMoodyCommon.LogLevel(bbMoodyCommon.DebugLevelError)
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
	config := &odoConfig.Protocol{}
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
		Logger.Error(moodyCommon.Object{"message": err.Error()})
		fmt.Println(err)
		Context.Done()
		os.Exit(0)
	}

	Node = srv.Node()

}
