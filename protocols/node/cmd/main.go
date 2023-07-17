package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	service "github.com/taubyte/odo/protocols/node/service"
)

func main() {
	ctx, ctxC := context.WithCancel(context.Background())
	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		fmt.Println("Exiting...")
		ctxC()
	}()

	srv, err := service.New(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}

	<-ctx.Done()

	srv.Close()
	os.Exit(0)
}
