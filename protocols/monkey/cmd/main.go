package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	moody "bitbucket.org/taubyte/go-moody-blues/common"
	service "github.com/taubyte/odo/protocols/monkey/service"
)

func main() {
	moody.LogLevel(moody.DebugLevelError)
	ctx, ctx_cancel := context.WithCancel(context.Background())

	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		fmt.Println("Exiting...")
		ctx_cancel()
	}()

	srv, err := service.New(ctx, nil)

	if err != nil {
		os.Exit(0)
	}

	<-ctx.Done()
	srv.Close()

	os.Exit(0)
}
