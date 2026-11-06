package stream

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nkeys"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// A real NATS server with JetStream in process, and franz-go's in-process Kafka
// cluster: the clients speak their wire protocols to them as to any other.

func kafkaCluster(t *testing.T) string {
	t.Helper()
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.DefaultNumPartitions(6), kfake.EnableSASL(),
		kfake.Superuser("SCRAM-SHA-512", "tau", "s3cret"),
		kfake.Superuser("SCRAM-SHA-256", "tau256", "s3cret"),
		kfake.Superuser("PLAIN", "plain", "s3cret"))
	require.NoError(t, err)
	t.Cleanup(cluster.Close)
	return strings.Join(cluster.ListenAddrs(), ",")
}

func natsServer(t *testing.T, opts natsserver.Options) string {
	t.Helper()
	opts.Host, opts.Port = "127.0.0.1", -1
	opts.JetStream, opts.StoreDir = true, t.TempDir()
	opts.NoLog, opts.NoSigs = true, true
	s, err := natsserver.NewServer(&opts)
	require.NoError(t, err)
	s.Start()
	t.Cleanup(s.Shutdown)
	require.True(t, s.ReadyForConnections(10*time.Second))
	return strings.TrimPrefix(s.ClientURL(), "nats://")
}

func contextFor(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(t.Context(), d)
}

func onLog(engine, host string, extra ...string) map[string]string {
	settings := map[string]string{
		specs.StreamKeyEngine: engine, specs.StreamKeyName: "orders", specs.StreamKeyHost: host,
		specs.StreamKeyTls: specs.StreamTlsDisable, specs.StreamCredentialsKeyName: "QmCreds",
	}
	for i := 0; i+1 < len(extra); i += 2 {
		settings[extra[i]] = extra[i+1]
	}
	return settings
}

// node is one node's consumers.
func node(t *testing.T, to Invoker, secret string) *Subscriptions {
	t.Helper()
	s, err := NewSubscriptions(to, vault{secret: secret})
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

// untilDelivered publishes a probe until a subscriber has it: a consumer starts at
// the end of the log, so what is published before it joined is not its to deliver.
func untilDelivered(t *testing.T, p *Publisher, grant invoke.Grant, to *recorder, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		require.NoError(t, p.Publish(t.Context(), grant, []byte("probe")))
		return len(to.bodiesFor(name)) > 0
	}, 60*time.Second, 500*time.Millisecond, "nothing reached %s", name)
}

// marked is what one subscriber received that the test published after the probe.
func marked(to *recorder, name string) map[string]int {
	out := map[string]int{}
	for _, b := range to.bodiesFor(name) {
		if strings.HasPrefix(b, "order:") {
			out[b]++
		}
	}
	return out
}

func deliversToEverySubscriber(t *testing.T, settings map[string]string, secret string) {
	to := &recorder{}
	mailer, audit := subscriber("mailer", settings), subscriber("audit", settings)
	subs := []worker.Subscriber{mailer, audit}

	// Two nodes holding the deployment, each running both subscribers' consumers.
	node(t, to, secret).Reconcile(subs)
	node(t, to, secret).Reconcile(subs)

	p, err := NewPublisher(subscribed{}, to, vault{secret: secret})
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })
	grant := invoke.Grant{Label: "orders", Handle: ordersHandle, Settings: settings}
	untilDelivered(t, p, grant, to, "mailer")
	untilDelivered(t, p, grant, to, "audit")

	for i := range 5 {
		require.NoError(t, p.Publish(t.Context(), grant, []byte("order:"+strconv.Itoa(i))))
	}
	for _, name := range []string{"mailer", "audit"} {
		require.Eventually(t, func() bool { return len(marked(to, name)) == 5 }, 30*time.Second, 50*time.Millisecond,
			"%s got %v", name, marked(to, name))
	}
	for _, c := range to.received() {
		require.Equal(t, "orders", c.label)
		require.NotEmpty(t, c.delivery, "where the message sits in the log")
	}
}

func TestAKafkaStreamReachesEverySubscriber(t *testing.T) {
	deliversToEverySubscriber(t, onLog(specs.StreamEngineKafka, kafkaCluster(t)), "tau:s3cret")
}

func TestANatsStreamReachesEverySubscriber(t *testing.T) {
	host := natsServer(t, natsserver.Options{Username: "tau", Password: "s3cret"})
	deliversToEverySubscriber(t, onLog(specs.StreamEngineNats, host), "tau:s3cret")
}

func TestEachKafkaMechanismLogsIn(t *testing.T) {
	host := kafkaCluster(t)
	for _, tc := range []struct{ sasl, secret string }{
		{"", "tau:s3cret"},
		{specs.StreamSaslScramSha512, "tau:s3cret"},
		{specs.StreamSaslScramSha256, "tau256:s3cret"},
		{specs.StreamSaslPlain, "plain:s3cret"},
	} {
		p := publisherOver(t, subscribed{}, &recorder{})
		p.logs = newLogs(vault{secret: tc.secret})
		grant := invoke.Grant{Handle: ordersHandle, Settings: onLog(specs.StreamEngineKafka, host, specs.StreamKeySasl, tc.sasl)}
		require.NoError(t, p.Publish(t.Context(), grant, []byte("x")), "sasl %q", tc.sasl)
	}
}

func TestAWrongKafkaPasswordIsRefusedAndTheConnectionDropped(t *testing.T) {
	host := kafkaCluster(t)
	l := newLogs(vault{secret: "tau:wrong"})
	t.Cleanup(func() { l.Close() })
	grant := invoke.Grant{Handle: ordersHandle, Settings: onLog(specs.StreamEngineKafka, host)}
	ctx, stop := contextFor(t, 10*time.Second)
	defer stop()
	require.Error(t, l.publish(ctx, grant, []byte("x")))
	require.Empty(t, l.open, "the next publish dials again and reads the secret again")
}

func TestANatsTokenLogsIn(t *testing.T) {
	host := natsServer(t, natsserver.Options{Authorization: "t0ken"})
	l := newLogs(vault{secret: "t0ken"})
	t.Cleanup(func() { l.Close() })
	grant := invoke.Grant{Handle: ordersHandle, Settings: onLog(specs.StreamEngineNats, host)}
	require.NoError(t, l.publish(t.Context(), grant, []byte("x")))
	require.NoError(t, l.publish(t.Context(), grant, []byte("y")), "over the connection kept")
	require.Len(t, l.open, 1)
}

func TestAFailingSubscriberIsOfferedTheMessageAgain(t *testing.T) {
	for _, tc := range []struct {
		engine string
		host   func(*testing.T) string
	}{
		{specs.StreamEngineKafka, kafkaCluster},
		{specs.StreamEngineNats, func(t *testing.T) string {
			return natsServer(t, natsserver.Options{Username: "tau", Password: "s3cret"})
		}},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			to := &recorder{}
			settings := onLog(tc.engine, tc.host(t))
			node(t, to, "tau:s3cret").Reconcile([]worker.Subscriber{subscriber("mailer", settings)})
			p, err := NewPublisher(subscribed{}, to, vault{secret: "tau:s3cret"})
			require.NoError(t, err)
			t.Cleanup(func() { p.Close() })
			grant := invoke.Grant{Handle: ordersHandle, Settings: settings}
			untilDelivered(t, p, grant, to, "mailer")

			to.mu.Lock()
			to.failures, to.failing = 2, "order:1"
			to.mu.Unlock()
			require.NoError(t, p.Publish(t.Context(), grant, []byte("order:1")))
			require.Eventually(t, func() bool { return marked(to, "mailer")["order:1"] == 3 }, 30*time.Second, 50*time.Millisecond,
				"twice refused and taken the third time: %v", marked(to, "mailer"))

			var ids []string
			for _, c := range to.received() {
				if c.body == "order:1" {
					ids = append(ids, c.delivery)
				}
			}
			require.Equal(t, ids[0], ids[2], "the same message each time: %v", ids)
		})
	}
}

func TestAKafkaMessageASubscriberNeverTakesIsPassedOver(t *testing.T) {
	to := &recorder{failures: maxAttempts}
	require.True(t, offer(t.Context(), subscriberHandler{invoker: to, to: &worker.Subscriber{}}, nil, "0-1"))
	require.Len(t, to.received(), maxAttempts)
}

func TestAnOfferEndsWithItsContext(t *testing.T) {
	ctx, stop := contextFor(t, time.Second)
	stop()
	to := &recorder{failures: 1}
	require.False(t, offer(ctx, subscriberHandler{invoker: to, to: &worker.Subscriber{}}, nil, "0-1"))
}

func TestReconcileStartsStopsAndRestarts(t *testing.T) {
	host := natsServer(t, natsserver.Options{Username: "tau", Password: "s3cret"})
	s := node(t, &recorder{}, "tau:s3cret")

	onNats := subscriber("mailer", onLog(specs.StreamEngineNats, host))
	onSwarm := subscriber("audit", distributed())
	s.Reconcile([]worker.Subscriber{onNats, onSwarm})
	require.Equal(t, 1, s.Running(), "the swarm's own engine has no consumer")

	before := s.running[subscriptionKey(&onNats)]
	moved := subscriber("mailer", onLog(specs.StreamEngineNats, host, specs.StreamKeyTls, specs.StreamTlsRequire))
	s.Reconcile([]worker.Subscriber{moved})
	require.Equal(t, 1, s.Running())
	require.NotSame(t, before, s.running[subscriptionKey(&moved)], "a changed stream is a new consumer")
	<-before.done

	s.Reconcile(nil)
	require.Zero(t, s.Running())

	require.NoError(t, s.Close())
	s.Reconcile([]worker.Subscriber{onNats})
	require.Zero(t, s.Running(), "nothing starts once closed")
}

func TestAConsumerThatCannotDialKeepsTrying(t *testing.T) {
	s := node(t, &recorder{}, "")
	s.Reconcile([]worker.Subscriber{subscriber("mailer", onLog(specs.StreamEngineKafka, "127.0.0.1:1"))})
	require.Equal(t, 1, s.Running(), "a missing secret is a failed dial, retried, not a dropped subscriber")
}

func TestAGroupIsTheSameOnEveryNodeAndDistinctPerSubscriber(t *testing.T) {
	mailer := subscriber("mailer", nil)
	require.Equal(t, groupOf(&mailer), groupOf(&mailer))
	require.True(t, strings.HasPrefix(groupOf(&mailer), "tau_shop_prod_mailer_"))

	nested := mailer
	nested.Target.Scope = []string{"eu"}
	require.NotEqual(t, groupOf(&mailer), groupOf(&nested), "one name in two namespaces")

	other := mailer
	other.Stream.Handle = "shop/prod/QmOther"
	require.NotEqual(t, groupOf(&mailer), groupOf(&other), "one subscriber of two streams")

	require.Equal(t, "a-b-c", clean("a.b c"))
}

func TestTLSIsVerifiedUnlessWeakened(t *testing.T) {
	full, err := tlsOf("")
	require.NoError(t, err)
	require.False(t, full.InsecureSkipVerify)

	required, err := tlsOf(specs.StreamTlsRequire)
	require.NoError(t, err)
	require.True(t, required.InsecureSkipVerify)

	off, err := tlsOf(specs.StreamTlsDisable)
	require.NoError(t, err)
	require.Nil(t, off)

	_, err = tlsOf("maybe")
	require.Error(t, err)
}

func TestAMisdeclaredLogIsRefusedAtDial(t *testing.T) {
	ctx := t.Context()
	for name, at := range map[string]broker{
		"no host":     {},
		"bad tls":     {host: "h:1", tls: "maybe"},
		"no colon":    {host: "h:1", tls: "disable", secret: "justapassword"},
		"bad sasl":    {host: "h:1", tls: "disable", secret: "u:p", sasl: "gssapi"},
		"spaces only": {host: " , "},
	} {
		_, err := kafkaEngine{}.dial(ctx, at)
		require.Error(t, err, "kafka: %s", name)
	}
	for name, at := range map[string]broker{
		"no host": {},
		"bad tls": {host: "h:1", tls: "maybe"},
		"bad jwt": {host: "h:1", tls: "disable", secret: "-----BEGIN NATS USER JWT-----\n"},
	} {
		_, err := natsEngine{}.dial(ctx, at)
		require.Error(t, err, "nats: %s", name)
	}
	_, err := dial(ctx, vault{}, invoke.Grant{Settings: map[string]string{specs.StreamKeyEngine: "rabbitmq"}}, publisherName)
	require.ErrorIs(t, err, ErrUnknownEngine)
}

func TestANatsCredsFileIsReadAsAJWTAndSeed(t *testing.T) {
	user, err := nkeys.CreateUser()
	require.NoError(t, err)
	seed, err := user.Seed()
	require.NoError(t, err)
	creds := fmt.Sprintf("-----BEGIN NATS USER JWT-----\neyJhbGciOiJlZDI1NTE5In0.e30.c2ln\n------END NATS USER JWT------\n\n"+
		"-----BEGIN USER NKEY SEED-----\n%s\n------END USER NKEY SEED------\n", seed)
	login, err := natsLogin(creds)
	require.NoError(t, err)
	require.NotNil(t, login)

	for secret, want := range map[string]bool{"": false, "u:p": true, "token": true} {
		login, err := natsLogin(secret)
		require.NoError(t, err)
		require.Equal(t, want, login != nil, "%q", secret)
	}
}

func TestBackoffDoublesToACap(t *testing.T) {
	require.Equal(t, time.Second, backoff(0))
	require.Equal(t, time.Second, backoff(1))
	require.Equal(t, 4*time.Second, backoff(3))
	require.Equal(t, 30*time.Second, backoff(20))
	require.Equal(t, []string{"a:1", "b:2"}, hosts(" a:1 ,, b:2"))
}

func TestAKafkaRebalanceDeliversNothingTwice(t *testing.T) {
	to := &recorder{slow: 150 * time.Millisecond}
	settings := onLog(specs.StreamEngineKafka, kafkaCluster(t))
	mailer := []worker.Subscriber{subscriber("mailer", settings)}
	node(t, to, "tau:s3cret").Reconcile(mailer)

	p, err := NewPublisher(subscribed{}, to, vault{secret: "tau:s3cret"})
	require.NoError(t, err)
	t.Cleanup(func() { p.Close() })
	grant := invoke.Grant{Handle: ordersHandle, Settings: settings}
	untilDelivered(t, p, grant, to, "mailer")

	// A second node joins while the first is mid-delivery, and a third later: each
	// moves partitions, and a message being delivered as its partition moves is the
	// one that would arrive twice.
	const sent = 40
	for i := range sent {
		require.NoError(t, p.Publish(t.Context(), grant, []byte("order:"+strconv.Itoa(i))))
	}
	require.Eventually(t, func() bool { return len(marked(to, "mailer")) >= 5 }, 30*time.Second, 10*time.Millisecond)
	node(t, to, "tau:s3cret").Reconcile(mailer)
	require.Eventually(t, func() bool { return len(marked(to, "mailer")) >= 15 }, 30*time.Second, 10*time.Millisecond)
	node(t, to, "tau:s3cret").Reconcile(mailer)

	require.Eventually(t, func() bool { return len(marked(to, "mailer")) == sent }, 60*time.Second, 50*time.Millisecond,
		"got %d of %d", len(marked(to, "mailer")), sent)
	time.Sleep(time.Second)
	for body, times := range marked(to, "mailer") {
		require.Equal(t, 1, times, "%s was delivered %d times", body, times)
	}
}
