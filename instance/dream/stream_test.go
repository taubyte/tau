//go:build dreaming

package dream_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance"
	nodeDream "github.com/taubyte/tau/instance/dream"
	"github.com/taubyte/tau/pkg/invoke"
)

// A stream's subscriber is invoked by the platform: projects/with/stream/* declare one
// function on both ends, delivering what it published to `/put/sessions/delivered`,
// so a read of that key is what proves a message arrived.

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
	publishTo(t, publishing, "orders", "order:1")
	deliveredAt(t, reading, "delivered", "order:1")
	publishTo(t, publishing, "orders", "order:2")
	deliveredAt(t, reading, "delivered", "order:2")
}

// natsServer runs a real nats-server with JetStream in process, answering its
// address and the server itself, which the test asks about consumers.
func natsServer(t *testing.T) (*natsserver.Server, string) {
	t.Helper()
	opts := natsserver.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(),
		Username: "tau", Password: "s3cret", NoLog: true, NoSigs: true}
	server, err := natsserver.NewServer(&opts)
	require.NoError(t, err)
	server.Start()
	t.Cleanup(server.Shutdown)
	require.True(t, server.ReadyForConnections(10*time.Second))
	return server, strings.TrimPrefix(server.ClientURL(), "nats://")
}

func TestANatsStreamIsDeliveredByItsConsumer(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)
	_, address := natsServer(t)
	streamOnLog(t, "with/stream/nats", "natscreds", "127.0.0.1:4222", address)
}

// logServices is what a node serving a stream on a log runs: a fleet's, and the
// secrets service its credential is read from.
var logServices = append(append([]string{}, fleetServices...), instance.Secrets)

// servingLog deploys a project whose streams live on a log the test started at
// address, and sets their credential.
func servingLog(t *testing.T, name, creds, declared, address string) *running {
	t.Helper()
	r := deployServing(t, streamPointedAt(t, name, declared, address), "orders.example.com", cacherWASM(t), nil, logServices...)
	require.NoError(t, r.node.Vault().Set(t.Context(), r.project, "prod", r.idOf(t, "secret", creds), []byte("tau:s3cret")))
	return r
}

// streamOnLog deploys, then publishes until the subscriber has a message: a consumer
// starts at the end of the log, so the first may precede it.
func streamOnLog(t *testing.T, name, creds, declared, address string) {
	t.Helper()
	r := servingLog(t, name, creds, declared, address)
	untilKeyed(t, r, "order:1", "orders", "delivered")
	publishTo(t, r, "orders", "order:2")
	deliveredAt(t, r, "delivered", "order:2")
}

// untilKeyed publishes message to stream until every key holds it.
func untilKeyed(t *testing.T, r *running, message, stream string, keys ...string) {
	t.Helper()
	var last string
	require.Eventually(t, func() bool {
		publishTo(t, r, stream, message)
		for _, key := range keys {
			got, err := read(t, r, key)
			last = key + ": " + got
			if err != nil || got != message {
				return false
			}
		}
		return true
	}, 2*time.Minute, time.Second, "%q never reached every subscriber: %s", message, &lastSaid{&last})
}

// read is the subscriber's key as the root function reads it.
func read(t *testing.T, r *running, key string) (string, error) {
	t.Helper()
	res, err := r.on(t.Context(), r.function("cacher"), http.MethodPost, "/get/sessions/"+key)
	if err != nil {
		return "", err
	}
	if res.Status != http.StatusOK {
		return fmt.Sprintf("%d %s", res.Status, res.Body), nil
	}
	return string(res.Body), nil
}

// deliveredAt waits until key holds message.
func deliveredAt(t *testing.T, r *running, key, message string) {
	t.Helper()
	var last string
	require.Eventually(t, func() bool {
		got, err := read(t, r, key)
		last = got
		return err == nil && got == message
	}, 60*time.Second, 250*time.Millisecond, "%q never reached %s: %s", message, key, &lastSaid{&last})
}

// publishTo has the root function publish message to stream.
func publishTo(t *testing.T, on *running, stream, message string) {
	t.Helper()
	var last string
	require.Eventually(t, func() bool {
		res, err := on.onCarrying(t.Context(), on.function("cacher"), http.MethodPost, "/publish/"+stream+"/", message)
		if err != nil {
			last = err.Error()
			return false
		}
		last = fmt.Sprintf("%d %s", res.Status, res.Body)
		return res.Status == 204
	}, 60*time.Second, 250*time.Millisecond, "the publish was refused: %s", &lastSaid{&last})
}

// streamPointedAt copies the named project with every stream's host replaced.
func streamPointedAt(t *testing.T, name, declared, address string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	require.NoError(t, os.CopyFS(dir, os.DirFS(projectDir(name))))

	files, err := filepath.Glob(filepath.Join(dir, "stream", "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, file := range files {
		yaml, err := os.ReadFile(file)
		require.NoError(t, err)
		rewritten := strings.Replace(string(yaml), "host: "+declared, "host: "+address, 1)
		require.NotEqual(t, string(yaml), rewritten, "%s names no host to point at the server", file)
		require.NoError(t, os.WriteFile(file, []byte(rewritten), 0o644))
	}
	return dir
}

func TestStreamsKeepTheirSubscribersApartAcrossNamespaces(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)
	server, address := natsServer(t)
	r := servingLog(t, "with/stream/namespaces", "natscreds", "127.0.0.1:4222", address)

	// Both subscribers of orders, one in each namespace and both called cacher, get it.
	untilKeyed(t, r, "order:1", "orders", "eu-orders", "us-orders")
	untilKeyed(t, r, "refund:1", "refunds", "us-refunds")

	// A refund reaches only the one subscriber of refunds, and nobody's orders key.
	publishTo(t, r, "refunds", "refund:2")
	deliveredAt(t, r, "us-refunds", "refund:2")
	for _, key := range []string{"eu-orders", "us-orders"} {
		got, err := read(t, r, key)
		require.NoError(t, err)
		require.Equal(t, "order:1", got, "%s took a refund", key)
	}

	// Seen from the log: one consumer per subscriber of each stream, named apart though
	// the two subscribers share a name.
	require.ElementsMatch(t, []int{2, 1}, []int{len(consumersOn(t, server, "orders")), len(consumersOn(t, server, "refunds"))})
	orders := consumersOn(t, server, "orders")
	require.NotEqual(t, orders[0], orders[1])
}

func TestANatsStreamIsSplitBetweenTheNodesHoldingIt(t *testing.T) {
	defer nodeDream.Runtimes(nodeDream.Runtimes()...)
	server, address := natsServer(t)
	cloud := deployFleetServing(t, 2, logServices, cacherWASM(t), nil,
		deployed{name: streamPointedAt(t, "with/stream/nats", "127.0.0.1:4222", address), host: "orders.example.com"}).fleets[0]
	first, second := cloud[0], cloud[1]
	require.NoError(t, first.node.Vault().Set(t.Context(), first.project, "prod", first.idOf(t, "secret", "natscreds"), []byte("tau:s3cret")))

	// Each node holding the deployment runs the subscriber's consumer: two connections
	// named for the one consumer. A deployment is held once something reached it.
	for _, n := range cloud {
		_, err := read(t, n, "delivered")
		require.NoError(t, err)
	}
	var group string
	require.Eventually(t, func() bool {
		names := consumerConnections(t, server)
		if len(names) != 2 || names[0] != names[1] {
			return false
		}
		group = names[0]
		return true
	}, 60*time.Second, 250*time.Millisecond, "both nodes never consumed: %v", consumerConnections(t, server))
	require.Equal(t, []string{group}, consumersOn(t, server, "orders"), "one consumer, shared")

	untilKeyed(t, first, "order:0", "orders", "delivered")
	settled := quiet(t, server, group)

	// Five from each node, each delivered once: the log counts exactly ten more
	// deliveries, none of them again.
	for i := range 5 {
		publishTo(t, first, "orders", fmt.Sprintf("order:a%d", i))
		publishTo(t, second, "orders", fmt.Sprintf("order:b%d", i))
	}
	now := quiet(t, server, group)
	require.Equal(t, settled.acked+10, now.acked, "every message taken")
	require.Equal(t, settled.delivered+10, now.delivered, "and delivered once")
	require.Equal(t, settled.redelivered, now.redelivered)
}

// jetStreamOn is a client of the test's own server, to ask it about consumers.
func jetStreamOn(t *testing.T, server *natsserver.Server) jetstream.JetStream {
	t.Helper()
	nc, err := nats.Connect(server.ClientURL(), nats.UserInfo("tau", "s3cret"))
	require.NoError(t, err)
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	require.NoError(t, err)
	return js
}

// consumersOn is the consumers of one stream, by name, sorted.
func consumersOn(t *testing.T, server *natsserver.Server, stream string) []string {
	t.Helper()
	s, err := jetStreamOn(t, server).Stream(t.Context(), stream)
	require.NoError(t, err)
	var out []string
	for name := range s.ConsumerNames(t.Context()).Name() {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// consumerConnections is the names of the connections a consumer opened, sorted; a
// consumer's connection is named for it, and a publisher's is not.
func consumerConnections(t *testing.T, server *natsserver.Server) []string {
	t.Helper()
	connz, err := server.Connz(&natsserver.ConnzOptions{})
	require.NoError(t, err)
	var out []string
	for _, c := range connz.Conns {
		if strings.HasPrefix(c.Name, "tau_") {
			out = append(out, c.Name)
		}
	}
	sort.Strings(out)
	return out
}

// settlement is where one consumer stands, as the server counts it.
type settlement struct {
	delivered, acked, redelivered uint64
	waiting                       uint64
}

// quiet waits until the consumer has nothing pending and nothing unacknowledged.
func quiet(t *testing.T, server *natsserver.Server, group string) settlement {
	t.Helper()
	js := jetStreamOn(t, server)
	var at settlement
	require.Eventually(t, func() bool {
		c, err := js.Consumer(t.Context(), "orders", group)
		if err != nil {
			return false
		}
		info, err := c.Info(t.Context())
		if err != nil {
			return false
		}
		at = settlement{
			delivered: info.Delivered.Consumer, acked: info.AckFloor.Consumer,
			redelivered: uint64(info.NumRedelivered), waiting: info.NumPending + uint64(info.NumAckPending),
		}
		return at.waiting == 0 && at.acked == at.delivered
	}, 60*time.Second, 250*time.Millisecond, "the consumer never settled: %+v", &at)
	return at
}
