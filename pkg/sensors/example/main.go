package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"
)

const (
	apiURL           = "https://9m3w96fj0.dep.tau.link/api/list"
	sensorServiceURL = "http://127.0.0.1:4217" // Default sensor service address
	pollInterval     = 10 * time.Second
)

// APIResponse represents the structure of the API response
type APIResponse struct {
	Count  int     `json:"count"`
	Values []Value `json:"values"`
}

// Value represents a single peer value in the API response
type Value struct {
	PeerID  string  `json:"peerId"`
	Address Address `json:"address"`
	Values  Metrics `json:"values"`
	Raw     string  `json:"raw"`
}

// Address represents the peer address
type Address struct {
	IP       string `json:"ip"`
	Port     string `json:"port"`
	Protocol string `json:"protocol"`
}

// Metrics represents the metric values - can contain multiple named metrics
// The "values" field in the API response is a map of metric names to MetricData
type Metrics map[string]MetricData

// MetricData represents the metric data
type MetricData struct {
	Current   int `json:"current"`
	SoftLimit int `json:"softLimit"`
	HardLimit int `json:"hardLimit"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		log.Println("Shutting down...")
		cancel()
	}()

	// Create collector
	collector := NewCollector(apiURL, sensorServiceURL, nil)

	// Get node ID from sensor service
	log.Println("Getting node ID from sensor service...")
	nodeID, err := collector.GetNodeID(ctx)
	if err != nil {
		log.Fatalf("Failed to get node info: %v", err)
	}
	log.Printf("Node ID: %s", nodeID)

	// Start periodic polling
	log.Printf("Starting to poll API every %v...", pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Do initial fetch
	if err := collector.CollectAndPush(ctx); err != nil {
		log.Printf("Initial fetch error: %v", err)
	}

	// Poll periodically
	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled, stopping...")
			return
		case <-ticker.C:
			if err := collector.CollectAndPush(ctx); err != nil {
				log.Printf("Fetch error: %v", err)
			}
		}
	}
}
