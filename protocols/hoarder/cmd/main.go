package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	service "github.com/taubyte/odo/protocols/hoarder/service"
)

func main() {

	fmt.Println("Create context")
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

	// Wait for context to close
	<-ctx.Done()

	srv.Close()
	os.Exit(0)
}
