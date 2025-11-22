package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/h2non/gock"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/sensors"
	sensorsv1connect "github.com/taubyte/tau/pkg/sensors/proto/gen/sensors/v1/sensorsv1connect"
	"gotest.tools/v3/assert"
)

func TestCollector_FetchMetrics(t *testing.T) {
	defer gock.Off()

	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	assert.Assert(t, serviceAddr != nil, "Sensor service address should not be nil")
	serviceURL := "http://" + serviceAddr.String()

	apiURL := "https://9m3w96fj0.dep.tau.link"
	mockResponse := APIResponse{
		Count: 2,
		Values: []Value{
			{
				PeerID: "12D3KooWET6PkvGhGoTLqpf9revpJ4Ry6Lu9ypL9iWKhJffwydm9",
				Address: Address{
					IP:       "107.191.34.226",
					Port:     "4242",
					Protocol: "tcp",
				},
				Values: Metrics{
					"metric": MetricData{
						Current:   60,
						SoftLimit: 63,
						HardLimit: 90,
					},
				},
				Raw: "/ip4/107.191.34.226/tcp/4242/p2p/12D3KooWET6PkvGhGoTLqpf9revpJ4Ry6Lu9ypL9iWKhJffwydm9",
			},
			{
				PeerID: "12D3KooWG7rhYz7b6JCuYbE7yeEn5UFTS1FbXv1oLgrtewN3cFSJ",
				Address: Address{
					IP:       "155.138.254.194",
					Port:     "4242",
					Protocol: "tcp",
				},
				Values: Metrics{
					"metric": MetricData{
						Current:   62,
						SoftLimit: 63,
						HardLimit: 90,
					},
				},
				Raw: "/ip4/155.138.254.194/tcp/4242/p2p/12D3KooWG7rhYz7b6JCuYbE7yeEn5UFTS1FbXv1oLgrtewN3cFSJ",
			},
		},
	}

	gock.New(apiURL).
		Get("/api/list").
		Reply(200).
		JSON(mockResponse)

	httpClient := &http.Client{
		Transport: gock.DefaultTransport,
		Timeout:   10 * time.Second,
	}

	collector := NewCollector(apiURL+"/api/list", serviceURL, httpClient)

	apiResp, err := collector.FetchMetrics(ctx)
	assert.NilError(t, err)
	assert.Equal(t, apiResp.Count, 2)
	assert.Equal(t, len(apiResp.Values), 2)
	assert.Equal(t, apiResp.Values[0].Values["metric"].Current, 60)
	assert.Equal(t, apiResp.Values[0].Values["metric"].SoftLimit, 63)
	assert.Equal(t, apiResp.Values[0].Values["metric"].HardLimit, 90)
	assert.Equal(t, apiResp.Values[1].Values["metric"].Current, 62)

	assert.Assert(t, gock.IsDone(), "All gock mocks should be called")
}

func TestCollector_PushValues(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	assert.Assert(t, serviceAddr != nil, "Sensor service address should not be nil")
	serviceURL := "http://" + serviceAddr.String()

	collector := NewCollector("", serviceURL, nil)

	values := []Value{
		{
			PeerID: "test-peer-1",
			Values: Metrics{
				"metric": MetricData{
					Current:   60,
					SoftLimit: 63,
					HardLimit: 90,
				},
			},
		},
		{
			PeerID: "test-peer-2",
			Values: Metrics{
				"metric": MetricData{
					Current:   62,
					SoftLimit: 63,
					HardLimit: 90,
				},
			},
		},
	}

	err = collector.PushValues(ctx, values)
	assert.NilError(t, err)

	registry := sensorService.Registry()

	value, exists, err := registry.Get("metric.current")
	assert.NilError(t, err)
	assert.Assert(t, exists, "metric.current should exist")
	assert.Equal(t, value, float64(62), "metric.current should be 62 (last value)")

	value, exists, err = registry.Get("metric.softLimit")
	assert.NilError(t, err)
	assert.Assert(t, exists, "metric.softLimit should exist")
	assert.Equal(t, value, float64(63), "metric.softLimit should be 63")

	value, exists, err = registry.Get("metric.hardLimit")
	assert.NilError(t, err)
	assert.Assert(t, exists, "metric.hardLimit should exist")
	assert.Equal(t, value, float64(90), "metric.hardLimit should be 90")
}

func TestCollector_CollectAndPush(t *testing.T) {
	defer gock.Off()

	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	assert.Assert(t, serviceAddr != nil, "Sensor service address should not be nil")
	serviceURL := "http://" + serviceAddr.String()

	time.Sleep(100 * time.Millisecond)

	mockNodeID := mockNode.ID().String()
	assert.Assert(t, mockNodeID != "", "Mock node ID should not be empty")

	collector := NewCollector("", serviceURL, nil)
	retrievedNodeID, err := collector.GetNodeID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, retrievedNodeID, mockNodeID, "Retrieved node ID should match mock node ID")

	apiURL := "https://9m3w96fj0.dep.tau.link"
	mockResponse := APIResponse{
		Count: 2,
		Values: []Value{
			{
				PeerID: retrievedNodeID,
				Address: Address{
					IP:       "107.191.34.226",
					Port:     "4242",
					Protocol: "tcp",
				},
				Values: Metrics{
					"metric": MetricData{
						Current:   60,
						SoftLimit: 63,
						HardLimit: 90,
					},
				},
				Raw: "/ip4/107.191.34.226/tcp/4242/p2p/" + retrievedNodeID,
			},
			{
				PeerID: "12D3KooWET6PkvGhGoTLqpf9revpJ4Ry6Lu9ypL9iWKhJffwydm9",
				Address: Address{
					IP:       "155.138.254.194",
					Port:     "4242",
					Protocol: "tcp",
				},
				Values: Metrics{
					"metric": MetricData{
						Current:   99,
						SoftLimit: 100,
						HardLimit: 100,
					},
				},
				Raw: "/ip4/155.138.254.194/tcp/4242/p2p/12D3KooWET6PkvGhGoTLqpf9revpJ4Ry6Lu9ypL9iWKhJffwydm9",
			},
		},
	}

	gock.New(apiURL).
		Get("/api/list").
		Reply(200).
		JSON(mockResponse)

	apiHTTPClient := &http.Client{
		Transport: gock.DefaultTransport,
		Timeout:   10 * time.Second,
	}

	sensorHTTPClient := &http.Client{
		Transport: &http.Transport{},
		Timeout:   10 * time.Second,
	}

	collector = &Collector{
		apiURL:       apiURL + "/api/list",
		httpClient:   apiHTTPClient,
		sensorClient: sensorsv1connect.NewSensorServiceClient(sensorHTTPClient, serviceURL),
	}

	err = collector.CollectAndPush(ctx)
	assert.NilError(t, err)

	registry := sensorService.Registry()

	value, exists, err := registry.Get("metric.current")
	assert.NilError(t, err)
	assert.Assert(t, exists, "metric.current should exist")
	assert.Equal(t, value, float64(60), "metric.current should be 60")

	value, exists, err = registry.Get("metric.softLimit")
	assert.NilError(t, err)
	assert.Assert(t, exists, "metric.softLimit should exist")
	assert.Equal(t, value, float64(63), "metric.softLimit should be 63")

	value, exists, err = registry.Get("metric.hardLimit")
	assert.NilError(t, err)
	assert.Assert(t, exists, "metric.hardLimit should exist")
	assert.Equal(t, value, float64(90), "metric.hardLimit should be 90")

	value, exists, err = registry.Get("metric.current")
	assert.NilError(t, err)
	assert.Assert(t, exists, "metric.current should exist")
	assert.Equal(t, value, float64(60), "metric.current should be 60 (not 99 from non-matching peer)")

	assert.Assert(t, gock.IsDone(), "All gock mocks should be called")
}

func TestCollector_GetNodeID(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	assert.Assert(t, serviceAddr != nil, "Sensor service address should not be nil")
	serviceURL := "http://" + serviceAddr.String()

	time.Sleep(500 * time.Millisecond)

	collector := NewCollector("", serviceURL, &http.Client{
		Timeout: 5 * time.Second,
	})

	nodeID, err := collector.GetNodeID(ctx)
	assert.NilError(t, err)
	mockNodeID := mockNode.ID().String()
	if nodeID != "" && mockNodeID != "" {
		assert.Equal(t, nodeID, mockNodeID, "Node ID should match the mock node ID")
	}
}

func TestCollector_FetchMetrics_ErrorCases(t *testing.T) {
	defer gock.Off()

	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	serviceURL := "http://" + serviceAddr.String()

	t.Run("non-200 status code", func(t *testing.T) {
		defer gock.Off()

		apiURL := "https://9m3w96fj0.dep.tau.link"
		gock.New(apiURL).
			Get("/api/list").
			Reply(500).
			BodyString("Internal Server Error")

		httpClient := &http.Client{
			Transport: gock.DefaultTransport,
			Timeout:   10 * time.Second,
		}

		collector := NewCollector(apiURL+"/api/list", serviceURL, httpClient)
		_, err := collector.FetchMetrics(ctx)
		assert.ErrorContains(t, err, "unexpected status code: 500")
	})

	t.Run("network error", func(t *testing.T) {
		defer gock.Off()

		apiURL := "https://invalid-url-that-does-not-exist.example.com"
		httpClient := &http.Client{
			Timeout: 1 * time.Second,
		}

		collector := NewCollector(apiURL+"/api/list", serviceURL, httpClient)
		_, err := collector.FetchMetrics(ctx)
		assert.ErrorContains(t, err, "failed to make request")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		defer gock.Off()

		apiURL := "https://9m3w96fj0.dep.tau.link"
		gock.New(apiURL).
			Get("/api/list").
			Reply(200).
			BodyString("invalid json {")

		httpClient := &http.Client{
			Transport: gock.DefaultTransport,
			Timeout:   10 * time.Second,
		}

		collector := NewCollector(apiURL+"/api/list", serviceURL, httpClient)
		_, err := collector.FetchMetrics(ctx)
		assert.ErrorContains(t, err, "failed to parse JSON")
	})
}

func TestCollector_PushValues_MultipleMetrics(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	serviceURL := "http://" + serviceAddr.String()

	collector := NewCollector("", serviceURL, nil)

	values := []Value{
		{
			PeerID: "test-peer",
			Values: Metrics{
				"metric": MetricData{
					Current:   60,
					SoftLimit: 63,
					HardLimit: 90,
				},
				"cpu": MetricData{
					Current:   50,
					SoftLimit: 80,
					HardLimit: 100,
				},
				"memory": MetricData{
					Current:   70,
					SoftLimit: 85,
					HardLimit: 95,
				},
			},
		},
	}

	err = collector.PushValues(ctx, values)
	assert.NilError(t, err)

	registry := sensorService.Registry()

	value, exists, err := registry.Get("metric.current")
	assert.NilError(t, err)
	assert.Assert(t, exists)
	assert.Equal(t, value, float64(60))

	value, exists, err = registry.Get("cpu.current")
	assert.NilError(t, err)
	assert.Assert(t, exists)
	assert.Equal(t, value, float64(50))

	value, exists, err = registry.Get("memory.current")
	assert.NilError(t, err)
	assert.Assert(t, exists)
	assert.Equal(t, value, float64(70))
}

func TestCollector_CollectAndPush_NoMatchingNode(t *testing.T) {
	defer gock.Off()

	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	serviceURL := "http://" + serviceAddr.String()

	time.Sleep(100 * time.Millisecond)

	collector := NewCollector("", serviceURL, nil)
	retrievedNodeID, err := collector.GetNodeID(ctx)
	assert.NilError(t, err)

	apiURL := "https://9m3w96fj0.dep.tau.link"
	mockResponse := APIResponse{
		Count: 1,
		Values: []Value{
			{
				PeerID: "different-peer-id", // Not matching
				Address: Address{
					IP:       "107.191.34.226",
					Port:     "4242",
					Protocol: "tcp",
				},
				Values: Metrics{
					"metric": MetricData{
						Current:   60,
						SoftLimit: 63,
						HardLimit: 90,
					},
				},
				Raw: "/ip4/107.191.34.226/tcp/4242/p2p/different-peer-id",
			},
		},
	}

	gock.New(apiURL).
		Get("/api/list").
		Reply(200).
		JSON(mockResponse)

	apiHTTPClient := &http.Client{
		Transport: gock.DefaultTransport,
		Timeout:   10 * time.Second,
	}

	sensorHTTPClient := &http.Client{
		Transport: &http.Transport{},
		Timeout:   10 * time.Second,
	}

	collector = &Collector{
		apiURL:       apiURL + "/api/list",
		httpClient:   apiHTTPClient,
		sensorClient: sensorsv1connect.NewSensorServiceClient(sensorHTTPClient, serviceURL),
	}

	err = collector.CollectAndPush(ctx)
	assert.ErrorContains(t, err, "no metrics found for node ID: "+retrievedNodeID)
}

func TestCollector_CollectAndPush_GetNodeIDError(t *testing.T) {
	ctx := context.Background()

	collector := NewCollector("", "http://invalid-url:9999", nil)

	err := collector.CollectAndPush(ctx)
	assert.ErrorContains(t, err, "failed to get node ID")
}

func TestCollector_CollectAndPush_FetchError(t *testing.T) {
	defer gock.Off()

	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	serviceURL := "http://" + serviceAddr.String()

	time.Sleep(100 * time.Millisecond)

	collector := NewCollector("", serviceURL, nil)
	_, err = collector.GetNodeID(ctx)
	assert.NilError(t, err)

	apiURL := "https://9m3w96fj0.dep.tau.link"
	gock.New(apiURL).
		Get("/api/list").
		Reply(500).
		BodyString("Server Error")

	apiHTTPClient := &http.Client{
		Transport: gock.DefaultTransport,
		Timeout:   10 * time.Second,
	}

	sensorHTTPClient := &http.Client{
		Transport: &http.Transport{},
		Timeout:   10 * time.Second,
	}

	collector = &Collector{
		apiURL:       apiURL + "/api/list",
		httpClient:   apiHTTPClient,
		sensorClient: sensorsv1connect.NewSensorServiceClient(sensorHTTPClient, serviceURL),
	}

	err = collector.CollectAndPush(ctx)
	assert.ErrorContains(t, err, "failed to fetch metrics")
}

func TestCollector_PushValues_Error(t *testing.T) {
	ctx := context.Background()

	collector := NewCollector("", "http://127.0.0.1:99999", &http.Client{
		Timeout: 1 * time.Second,
	})

	values := []Value{
		{
			PeerID: "test-peer",
			Values: Metrics{
				"metric": MetricData{
					Current:   60,
					SoftLimit: 63,
					HardLimit: 90,
				},
			},
		},
	}

	err := collector.PushValues(ctx, values)
	assert.ErrorContains(t, err, "failed to push")
}

func TestCollector_GetNodeID_Error(t *testing.T) {
	ctx := context.Background()

	collector := NewCollector("", "http://invalid-url:9999", nil)

	_, err := collector.GetNodeID(ctx)
	assert.ErrorContains(t, err, "failed to get node info")
}

func TestNewCollector_WithNilHTTPClient(t *testing.T) {
	collector := NewCollector("http://example.com", "http://sensor:4217", nil)
	assert.Assert(t, collector != nil)
	assert.Assert(t, collector.httpClient != nil)
	assert.Equal(t, collector.httpClient.Timeout, 10*time.Second)
}

func TestCollector_FetchMetrics_EmptyResponse(t *testing.T) {
	defer gock.Off()

	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	serviceURL := "http://" + serviceAddr.String()

	apiURL := "https://9m3w96fj0.dep.tau.link"
	mockResponse := APIResponse{
		Count:  0,
		Values: []Value{},
	}

	gock.New(apiURL).
		Get("/api/list").
		Reply(200).
		JSON(mockResponse)

	httpClient := &http.Client{
		Transport: gock.DefaultTransport,
		Timeout:   10 * time.Second,
	}

	collector := NewCollector(apiURL+"/api/list", serviceURL, httpClient)

	apiResp, err := collector.FetchMetrics(ctx)
	assert.NilError(t, err)
	assert.Equal(t, apiResp.Count, 0)
	assert.Equal(t, len(apiResp.Values), 0)
}

func TestCollector_FetchMetrics_InvalidURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockNode := peer.Mock(context.Background())
	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	serviceURL := "http://" + serviceAddr.String()

	collector := NewCollector("http://[invalid-url", serviceURL, nil)

	_, err = collector.FetchMetrics(ctx)
	assert.ErrorContains(t, err, "failed to create request")
}

func TestCollector_PushValues_EmptyValues(t *testing.T) {
	ctx := context.Background()
	mockNode := peer.Mock(ctx)

	sensorService, err := sensors.New(mockNode, sensors.WithPort(0))
	assert.NilError(t, err)

	serviceAddr := sensorService.Addr()
	serviceURL := "http://" + serviceAddr.String()

	collector := NewCollector("", serviceURL, nil)

	err = collector.PushValues(ctx, []Value{})
	assert.NilError(t, err)
}
