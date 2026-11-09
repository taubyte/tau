//go:build dreaming && docker_integration

package dream_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
)

// projects/with/stream/kafka against a real Kafka-protocol broker in docker:
// Redpanda, started open, given a SCRAM user through its admin API and then told to
// require SASL, so the stream's credential and mechanism are what get it in.

const redpandaImage = "redpandadata/redpanda:v24.2.4"

func TestAnExternalKafkaStreamIsDeliveredByItsConsumer(t *testing.T) {
	kafkaPort, adminPort := freePort(t), freePort(t)
	startRedpanda(t, kafkaPort, adminPort)
	streamOnLog(t, "with/stream/kafka", "kafkacreds", "127.0.0.1:9092", fmt.Sprintf("127.0.0.1:%d", kafkaPort))
}

func startRedpanda(t *testing.T, kafkaPort, adminPort int) {
	t.Helper()
	ctx := t.Context()

	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err)
	require.NoError(t, backend.Image(redpandaImage).Pull(ctx), "pulling %s", redpandaImage)

	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image: redpandaImage,
		Command: []string{"redpanda", "start", "--mode", "dev-container", "--smp", "1",
			"--kafka-addr", "PLAINTEXT://0.0.0.0:9092",
			"--advertise-kafka-addr", fmt.Sprintf("PLAINTEXT://127.0.0.1:%d", kafkaPort)},
		Network: &core.NetworkConfig{
			Mode: "bridge",
			PortMappings: []core.PortMapping{
				{HostPort: kafkaPort, ContainerPort: 9092, Protocol: "tcp", HostIP: "127.0.0.1"},
				{HostPort: adminPort, ContainerPort: 9644, Protocol: "tcp", HostIP: "127.0.0.1"},
			},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx := context.Background()
		backend.Stop(ctx, id)
		backend.Remove(ctx, id)
	})
	require.NoError(t, backend.Start(ctx, id))

	admin := fmt.Sprintf("http://127.0.0.1:%d", adminPort)
	var last string
	require.Eventually(t, func() bool {
		status, body := call(ctx, http.MethodGet, admin+"/v1/status/ready", "")
		last = fmt.Sprintf("%d %s", status, body)
		return status == http.StatusOK
	}, 2*time.Minute, time.Second, "redpanda never became ready: %s", &lastSaid{&last})

	status, body := call(ctx, http.MethodPost, admin+"/v1/security/users",
		`{"username":"tau","password":"s3cret","algorithm":"SCRAM-SHA-256"}`)
	require.Equal(t, http.StatusOK, status, "creating the user: %s", body)
	status, body = call(ctx, http.MethodPut, admin+"/v1/cluster_config",
		`{"upsert":{"enable_sasl":true,"superusers":["tau"]},"remove":[]}`)
	require.Equal(t, http.StatusOK, status, "requiring sasl: %s", body)
}

// call is one admin API request, answering its status and body.
func call(ctx context.Context, method, url, body string) (int, string) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBufferString(body))
	if err != nil {
		return 0, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer res.Body.Close()
	var out bytes.Buffer
	out.ReadFrom(res.Body)
	return res.StatusCode, out.String()
}
