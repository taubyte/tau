package stream

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
)

// A NATS stream is a JetStream stream of the stream's name, on the subject of the
// same name. The credential is a .creds file's contents, "user:password", or a
// token; a subscriber is a durable consumer delivering what is published from the
// moment it is made, and the server redelivers what is not acknowledged.

type natsEngine struct{}

// natsAckWait is how long the server waits for an answer before redelivering: past
// the longest a subscriber may run.
const natsAckWait = deliverWithin + 10*time.Second

func (natsEngine) dial(_ context.Context, at broker) (conn, error) {
	servers := hosts(at.host)
	if len(servers) == 0 {
		return nil, errors.New("stream: a nats stream names no host")
	}
	for i, s := range servers {
		if !strings.Contains(s, "://") {
			servers[i] = "nats://" + s
		}
	}

	opts := []nats.Option{nats.Name(at.name), nats.MaxReconnects(-1)}
	protect, err := tlsOf(at.tls)
	if err != nil {
		return nil, err
	}
	if protect != nil {
		opts = append(opts, nats.Secure(protect))
	}
	login, err := natsLogin(at.secret)
	if err != nil {
		return nil, err
	}
	if login != nil {
		opts = append(opts, login)
	}

	nc, err := nats.Connect(strings.Join(servers, ","), opts...)
	if err != nil {
		return nil, fmt.Errorf("stream: nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("stream: nats: %w", err)
	}
	return &natsConn{nc: nc, js: js}, nil
}

// natsLogin reads the credential in whichever of its three shapes it was written.
func natsLogin(secret string) (nats.Option, error) {
	switch {
	case secret == "":
		return nil, nil
	case strings.Contains(secret, "-----BEGIN NATS USER JWT-----"):
		jwt, err := nkeys.ParseDecoratedJWT([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("stream: the nats credential's jwt: %w", err)
		}
		keys, err := nkeys.ParseDecoratedNKey([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("stream: the nats credential's seed: %w", err)
		}
		seed, err := keys.Seed()
		if err != nil {
			return nil, fmt.Errorf("stream: the nats credential's seed: %w", err)
		}
		return nats.UserJWTAndSeed(jwt, string(seed)), nil
	default:
		if user, password, ok := strings.Cut(secret, ":"); ok {
			return nats.UserInfo(user, password), nil
		}
		return nats.Token(secret), nil
	}
}

type natsConn struct {
	nc *nats.Conn
	js jetstream.JetStream

	// made is the streams this connection already made sure of.
	made sync.Map
}

func (c *natsConn) publish(ctx context.Context, topic string, message []byte) error {
	if err := c.ensure(ctx, topic); err != nil {
		return err
	}
	_, err := c.js.Publish(ctx, topic, message)
	return err
}

// ensure creates the stream if it does not exist. One that does is used as it is:
// its limits and retention are its owner's.
func (c *natsConn) ensure(ctx context.Context, topic string) error {
	if _, done := c.made.Load(topic); done {
		return nil
	}
	_, err := c.js.Stream(ctx, topic)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		_, err = c.js.CreateStream(ctx, jetstream.StreamConfig{Name: topic, Subjects: []string{topic}})
		if errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
			err = nil
		}
	}
	if err != nil {
		return fmt.Errorf("stream: nats stream %s: %w", topic, err)
	}
	c.made.Store(topic, struct{}{})
	return nil
}

func (c *natsConn) consume(ctx context.Context, topic, group string, to handler, drain <-chan struct{}) error {
	if err := c.ensure(ctx, topic); err != nil {
		return err
	}
	consumer, err := c.js.CreateOrUpdateConsumer(ctx, topic, jetstream.ConsumerConfig{
		Durable:       group,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverNewPolicy,
		AckWait:       natsAckWait,
		MaxDeliver:    maxAttempts,
	})
	if err != nil {
		return fmt.Errorf("stream: nats consumer %s on %s: %w", group, topic, err)
	}

	taking, stop := drained(ctx, drain)
	defer stop()
	for ctx.Err() == nil {
		if taking.Err() != nil {
			return errDrained
		}
		if err := c.fetch(ctx, taking, consumer, to); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// fetch takes what is waiting, up to a few seconds' worth, under taking, and settles
// each message under ctx.
func (c *natsConn) fetch(ctx, taking context.Context, consumer jetstream.Consumer, to handler) error {
	wait, stop := context.WithTimeout(taking, 5*time.Second)
	defer stop()
	batch, err := consumer.Fetch(16, jetstream.FetchContext(wait))
	if err != nil {
		if taking.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("stream: nats fetch: %w", err)
	}

	for msg := range batch.Messages() {
		id, attempt := "", 1
		if meta, err := msg.Metadata(); err == nil {
			id = strconv.FormatUint(meta.Sequence.Stream, 10)
			attempt = int(meta.NumDelivered)
		}
		if err := to.handle(ctx, msg.Data(), id); err != nil {
			logger.Warnf("stream: delivery %s, attempt %d: %s", id, attempt, err)
			if err := msg.NakWithDelay(backoff(attempt)); err != nil {
				logger.Warnf("stream: returning %s: %s", id, err)
			}
			continue
		}
		if err := msg.Ack(); err != nil {
			logger.Warnf("stream: acknowledging %s: %s", id, err)
		}
	}
	if err := batch.Error(); err != nil && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, nats.ErrTimeout) && !errors.Is(err, jetstream.ErrNoMessages) {
		return fmt.Errorf("stream: nats fetch: %w", err)
	}
	return nil
}

func (c *natsConn) Close() error {
	c.nc.Close()
	return nil
}
