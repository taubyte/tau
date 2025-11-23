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

type APIResponse struct {
	Count  int     `json:"count"`
	Values []Value `json:"values"`
}

type Value struct {
	PeerID  string  `json:"peerId"`
	Address Address `json:"address"`
	Values  Metrics `json:"values"`
	Raw     string  `json:"raw"`
}

type Address struct {
	IP       string `json:"ip"`
	Port     string `json:"port"`
	Protocol string `json:"protocol"`
}

type Metrics map[string]MetricData

type MetricData struct {
	Current   int `json:"current"`
	SoftLimit int `json:"softLimit"`
	HardLimit int `json:"hardLimit"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		log.Println("Shutting down...")
		cancel()
	}()

	collector := NewCollector(apiURL, sensorServiceURL, nil)

	log.Println("Getting node ID from sensor service...")
	nodeID, err := collector.GetNodeID(ctx)
	if err != nil {
		log.Fatalf("Failed to get node info: %v", err)
	}
	log.Printf("Node ID: %s", nodeID)

	log.Printf("Starting to poll API every %v...", pollInterval)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	if err := collector.CollectAndPush(ctx); err != nil {
		log.Printf("Initial fetch error: %v", err)
	}

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
