package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/mcp"
)

func main() {
	flag.Parse()

	// Create context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create multiverse
	multiverse, err := dream.New(ctx, dream.LoadPersistent())
	if err != nil {
		log.Fatalf("Failed to create multiverse: %v", err)
	}
	defer multiverse.Close()

	// Create MCP service
	service, err := mcp.New(multiverse, nil)
	if err != nil {
		log.Fatalf("Failed to create MCP service: %v", err)
	}

	// Start MCP server
	go service.Server().Start()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Dream MCP server started. Press Ctrl+C to stop.")
	<-sigChan

	log.Println("Shutting down Dream MCP server...")
	cancel()
}
