//go:build dreaming

package dream_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
	storageService "github.com/taubyte/tau/services/storage"
)

// A stream's subscriber is invoked by the platform: projects/with/stream/* declare one
// function on both ends, delivering what it published to `/put/sessions/delivered`,
// so a read of that key is what proves a message arrived.

// delivered waits until the subscriber has written what was published.
func delivered(t *testing.T, reading *running, message string) {
	t.Helper()
	var last string
	require.Eventually(t, func() bool {
		res, err := reading.on(t.Context(), reading.function("cacher"), http.MethodPost, "/get/sessions/delivered")
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", res.Status, res.Body)
		return res.Status == 200 && string(res.Body) == message
	}, 60*time.Second, 250*time.Millisecond, "%q never reached the subscriber: %s", message, &lastSaid{&last})
}

// publish has the guest publish message, retried until the deployment is served.
func publish(t *testing.T, on *running, message string) {
	t.Helper()
	var last string
	require.Eventually(t, func() bool {
		res, err := on.onCarrying(t.Context(), on.function("cacher"), http.MethodPost, "/publish/orders/", message)
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", res.Status, res.Body)
		return res.Status == 204
	}, 60*time.Second, 250*time.Millisecond, "the publish was refused: %s", &lastSaid{&last})
}

func TestAPublishedMessageReachesItsSubscriberAcrossTheSwarm(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	cloud := deployFleet(t, 2, "with/stream/distributed", "orders.example.com", cacherWASM(t))
	publishing, reading := cloud[0], cloud[1]

	var stream invoke.Grant
	require.Eventually(t, func() bool {
		spec, err := reading.node.Worker().Resolve(t.Context(), reading.function("cacher"))
		if err != nil {
			return false
		}
		for _, g := range spec.Reaches {
			if g.Kind == "stream" {
				stream = g
			}
		}
		return stream.Handle != ""
	}, 60*time.Second, 500*time.Millisecond, "the project never reached the second node")
	require.Equal(t, reading.project+"/prod/"+reading.idOf(t, "stream", "orders"), stream.Handle,
		"the resolved id, not the label")
	require.True(t, stream.Allows("publish"))
	require.True(t, stream.Allows("subscribe"))
	require.Equal(t, "distributed", stream.Settings["engine"])

	// Published on one node, read back on the other: the publishing node invoked the
	// subscriber through placement and it wrote the replicated cache.
	publish(t, publishing, "order:1")
	delivered(t, reading, "order:1")
	publish(t, publishing, "order:2")
	delivered(t, reading, "order:2")
}

func TestANatsStreamIsDeliveredByItsConsumer(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)

	opts := natsserver.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(),
		Username: "tau", Password: "s3cret", NoLog: true, NoSigs: true}
	server, err := natsserver.NewServer(&opts)
	require.NoError(t, err)
	server.Start()
	t.Cleanup(server.Shutdown)
	require.True(t, server.ReadyForConnections(10*time.Second))

	streamOnLog(t, "with/stream/nats", "127.0.0.1:4222", strings.TrimPrefix(server.ClientURL(), "nats://"))
}

// streamOnLog deploys a project whose stream lives on a log the test started at
// address, sets its credential, and publishes until the subscriber has a message:
// a consumer starts at the end of the log, so the first may precede it.
func streamOnLog(t *testing.T, name, declared, address string) {
	t.Helper()
	r := deployServing(t, streamPointedAt(t, name, declared, address), "orders.example.com", cacherWASM(t), nil,
		instance.Register, instance.Worker, instance.Discovery, instance.Gateway, instance.Database, instance.Primitives,
		instance.Secrets, storageService.Descriptor.Name)

	creds := map[string]string{"with/stream/nats": "natscreds", "with/stream/kafka": "kafkacreds"}[name]
	require.NoError(t, r.node.Vault().Set(t.Context(), r.project, "prod", r.idOf(t, "secret", creds), []byte("tau:s3cret")))

	var last string
	require.Eventually(t, func() bool {
		publish(t, r, "order:1")
		res, err := r.on(t.Context(), r.function("cacher"), http.MethodPost, "/get/sessions/delivered")
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", res.Status, res.Body)
		return res.Status == 200 && string(res.Body) == "order:1"
	}, 2*time.Minute, time.Second, "nothing the guest published reached it as a subscriber: %s", &lastSaid{&last})

	publish(t, r, "order:2")
	delivered(t, r, "order:2")
}

// streamPointedAt copies the named project with its stream's host replaced.
func streamPointedAt(t *testing.T, name, declared, address string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.CopyFS(dir, os.DirFS(projectDir(name))))

	file := filepath.Join(dir, "stream", "orders.yaml")
	yaml, err := os.ReadFile(file)
	require.NoError(t, err)
	rewritten := strings.Replace(string(yaml), "host: "+declared, "host: "+address, 1)
	require.NotEqual(t, string(yaml), rewritten, "the project names no host to point at the server")
	require.NoError(t, os.WriteFile(file, []byte(rewritten), 0o644))
	return dir
}
