package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	sensorsv1 "github.com/taubyte/tau/pkg/sensors/proto/gen/sensors/v1"
	sensorsv1connect "github.com/taubyte/tau/pkg/sensors/proto/gen/sensors/v1/sensorsv1connect"
)

// Collector handles fetching metrics from an API and pushing them to a sensor service
type Collector struct {
	apiURL       string
	httpClient   *http.Client
	sensorClient sensorsv1connect.SensorServiceClient
}

// NewCollector creates a new collector instance
func NewCollector(apiURL string, sensorServiceURL string, httpClient *http.Client) *Collector {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	sensorClient := sensorsv1connect.NewSensorServiceClient(
		httpClient,
		sensorServiceURL,
	)

	return &Collector{
		apiURL:       apiURL,
		httpClient:   httpClient,
		sensorClient: sensorClient,
	}
}

// FetchMetrics fetches metrics from the API
func (c *Collector) FetchMetrics(ctx context.Context) (*APIResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &apiResp, nil
}

// PushValues pushes metrics to the sensor service
// Handles multiple named metrics in the values map
func (c *Collector) PushValues(ctx context.Context, values []Value) error {
	fmt.Printf("[%s] Pushing values\n", time.Now().Format(time.RFC3339))
	for _, value := range values {
		// Iterate through all named metrics in the values map
		for metricName, metricData := range value.Values {
			// Push metric.current
			metricPath := fmt.Sprintf("%s.current", metricName)
			fmt.Printf("Pushing value: %s = %.2f\n", metricPath, float64(metricData.Current))
			if err := c.push(ctx, metricPath, float64(metricData.Current)); err != nil {
				return fmt.Errorf("failed to push %s: %w", metricPath, err)
			}

			// Push metric.softLimit
			metricPath = fmt.Sprintf("%s.softLimit", metricName)
			fmt.Printf("Pushing value: %s = %.2f\n", metricPath, float64(metricData.SoftLimit))
			if err := c.push(ctx, metricPath, float64(metricData.SoftLimit)); err != nil {
				return fmt.Errorf("failed to push %s: %w", metricPath, err)
			}

			// Push metric.hardLimit
			metricPath = fmt.Sprintf("%s.hardLimit", metricName)
			fmt.Printf("Pushing value: %s = %.2f\n", metricPath, float64(metricData.HardLimit))
			if err := c.push(ctx, metricPath, float64(metricData.HardLimit)); err != nil {
				return fmt.Errorf("failed to push %s: %w", metricPath, err)
			}
		}
	}

	return nil
}

// CollectAndPush fetches metrics from the API and pushes them to the sensor service
// Only processes metrics for peers that match the current node ID
func (c *Collector) CollectAndPush(ctx context.Context) error {
	// Get the current node ID
	nodeID, err := c.GetNodeID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get node ID: %w", err)
	}

	apiResp, err := c.FetchMetrics(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch metrics: %w", err)
	}

	// Process only values for the current node
	var matchingValues []Value
	for _, value := range apiResp.Values {
		if value.PeerID == nodeID {
			matchingValues = append(matchingValues, value)
		}
	}

	if len(matchingValues) == 0 {
		return fmt.Errorf("no metrics found for node ID: %s", nodeID)
	}

	if err := c.PushValues(ctx, matchingValues); err != nil {
		return fmt.Errorf("failed to push metrics: %w", err)
	}

	return nil
}

// GetNodeID retrieves the node ID from the sensor service
func (c *Collector) GetNodeID(ctx context.Context) (string, error) {
	req := connect.NewRequest(&sensorsv1.NodeInfoRequest{})
	resp, err := c.sensorClient.NodeInfo(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to get node info: %w", err)
	}

	return resp.Msg.GetNodeId(), nil
}

// push pushes a single metric value to the sensor service via Connect RPC
func (c *Collector) push(ctx context.Context, name string, value float64) error {
	req := connect.NewRequest(&sensorsv1.PushValueRequest{
		Name:  name,
		Value: value,
	})
	_, err := c.sensorClient.PushValue(ctx, req)
	return err
}
