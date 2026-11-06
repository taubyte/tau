package stream

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// A stream on Kafka or NATS lives in somebody else's log, which keeps it: a
// publish is written there and each subscriber has its own consumer, named the same
// on every node, so the log hands each message to one of them. A message is
// acknowledged once the subscriber answered 2xx, which is at least once.

// engine is one external log a stream may live on.
type engine interface {
	dial(ctx context.Context, at broker) (conn, error)
}

// conn is one connection to a log.
type conn interface {
	publish(ctx context.Context, topic string, message []byte) error

	// consume hands each message published from now on to to, as the consumer
	// named group, until ctx ends or the connection fails.
	consume(ctx context.Context, topic, group string, to handler) error

	Close() error
}

// handler is what a consumer hands a message to; id is where it sits in the log.
type handler interface {
	handle(ctx context.Context, message []byte, id string) error
}

var engines = map[string]engine{
	specs.StreamEngineKafka: kafkaEngine{},
	specs.StreamEngineNats:  natsEngine{},
}

// broker is where a log is and how to log in, read off a grant and the vault, and
// the name the connection gives itself there: a consumer's, so whoever looks at the
// log sees which subscriber each connection serves.
type broker struct {
	host, tls, sasl string
	secret          string
	name            string
}

// publisherName is what a publishing connection calls itself.
const publisherName = "tau"

const (
	// maxAttempts is how often a message is offered to a subscriber that fails it
	// before it is passed over, so one bad message cannot stop a stream.
	maxAttempts = 5

	// retryEvery is how long a consumer whose connection failed waits to dial again.
	retryEvery = 5 * time.Second
)

// backoff is the wait before the attempt after this one: 1s, 2s, 4s, capped.
func backoff(attempt int) time.Duration {
	return min(time.Second<<max(attempt-1, 0), 30*time.Second)
}

// logs holds this node's publishing connections, one per log and login.
type logs struct {
	secrets Secrets

	mu   sync.Mutex
	open map[string]conn
}

func newLogs(secrets Secrets) *logs { return &logs{secrets: secrets, open: map[string]conn{}} }

func (l *logs) publish(ctx context.Context, grant invoke.Grant, message []byte) error {
	key := connKey(grant)
	c, err := l.conn(ctx, key, grant)
	if err != nil {
		return err
	}
	if err := c.publish(ctx, grant.Settings[specs.StreamKeyName], message); err != nil {
		// Dropped so the next publish dials again and reads the secret again, which is
		// how a rotated credential is picked up.
		l.drop(key, c)
		return err
	}
	return nil
}

func (l *logs) conn(ctx context.Context, key string, grant invoke.Grant) (conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.open[key]; ok {
		return c, nil
	}
	c, err := dial(ctx, l.secrets, grant, publisherName)
	if err != nil {
		return nil, err
	}
	l.open[key] = c
	return c, nil
}

func (l *logs) drop(key string, c conn) {
	l.mu.Lock()
	if l.open[key] == c {
		delete(l.open, key)
	}
	l.mu.Unlock()
	c.Close()
}

func (l *logs) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	for _, c := range l.open {
		err = errors.Join(err, c.Close())
	}
	clear(l.open)
	return err
}

// connKey is what one connection serves: the deployment the credential belongs to,
// the engine and everything about reaching it.
func connKey(grant invoke.Grant) string {
	project, environment, _ := specs.SplitHandle(grant.Handle)
	s := grant.Settings
	return strings.Join([]string{project, environment, s[specs.StreamKeyEngine], s[specs.StreamKeyHost],
		s[specs.StreamKeyTls], s[specs.StreamKeySasl], s[specs.StreamCredentialsKeyName]}, "\x00")
}

// dial opens a connection to the log a grant names, reading its credential now.
func dial(ctx context.Context, secrets Secrets, grant invoke.Grant, name string) (conn, error) {
	s := grant.Settings
	engine := s[specs.StreamKeyEngine]
	e, ok := engines[engine]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEngine, engine)
	}

	at := broker{host: s[specs.StreamKeyHost], tls: s[specs.StreamKeyTls], sasl: s[specs.StreamKeySasl], name: name}
	if reference := s[specs.StreamCredentialsKeyName]; reference != "" {
		project, environment, _ := specs.SplitHandle(grant.Handle)
		secret, err := secrets.Secret(ctx, project, environment, reference)
		if err != nil {
			return nil, fmt.Errorf("stream: reading the credentials for %s: %w", grant.Label, err)
		}
		at.secret = secret
	}
	return e.dial(ctx, at)
}

// tlsOf is the connection's protection. Absent is verify-full, the hostname taken
// from the address by each client.
func tlsOf(mode string) (*tls.Config, error) {
	switch mode {
	case "", specs.StreamTlsVerifyFull:
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	case specs.StreamTlsRequire:
		return &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, nil
	case specs.StreamTlsDisable:
		return nil, nil
	default:
		return nil, fmt.Errorf("stream: %q is not a tls mode", mode)
	}
}

// hosts is an address list, host:port separated by commas.
func hosts(host string) []string {
	var out []string
	for _, h := range strings.Split(host, ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// groupOf is the consumer a subscriber reads a log as, the same on every node so the
// log splits the messages between them. Readable for whoever looks at the log, with
// a digest of the full address because a name repeats across namespaces.
func groupOf(sub *worker.Subscriber) string {
	t := sub.Target
	sum := sha256.Sum256([]byte(strings.Join([]string{t.Project, t.Environment, strings.Join(t.Scope, "/"),
		t.Kind, t.Name, sub.Stream.Handle}, "\x00")))
	return strings.Join([]string{"tau", clean(t.Project), clean(t.Environment), clean(t.Name), hex.EncodeToString(sum[:6])}, "_")
}

// clean keeps what every log accepts in a consumer's name.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, s)
}
