package stream

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/taubyte/tau/pkg/specs"
)

// A Kafka stream is a topic of the stream's name. The credential is "user:password",
// presented as the stream's sasl says; a subscriber is a consumer group, starting at
// the end of the topic the first time it joins.

type kafkaEngine struct{}

func (kafkaEngine) dial(_ context.Context, at broker) (conn, error) {
	seeds := hosts(at.host)
	if len(seeds) == 0 {
		return nil, errors.New("stream: a kafka stream names no host")
	}
	opts := []kgo.Opt{kgo.SeedBrokers(seeds...), kgo.ClientID(at.name)}

	protect, err := tlsOf(at.tls)
	if err != nil {
		return nil, err
	}
	if protect != nil {
		opts = append(opts, kgo.DialTLSConfig(protect))
	}

	if at.secret != "" {
		user, password, ok := strings.Cut(at.secret, ":")
		if !ok {
			return nil, errors.New("stream: a kafka credential is user:password")
		}
		switch at.sasl {
		case "", specs.StreamSaslScramSha512:
			opts = append(opts, kgo.SASL(scram.Auth{User: user, Pass: password}.AsSha512Mechanism()))
		case specs.StreamSaslScramSha256:
			opts = append(opts, kgo.SASL(scram.Auth{User: user, Pass: password}.AsSha256Mechanism()))
		case specs.StreamSaslPlain:
			opts = append(opts, kgo.SASL(plain.Auth{User: user, Pass: password}.AsMechanism()))
		default:
			return nil, fmt.Errorf("stream: %q is not a sasl mechanism", at.sasl)
		}
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("stream: kafka: %w", err)
	}
	return &kafkaConn{opts: opts, client: client}, nil
}

type kafkaConn struct {
	opts   []kgo.Opt
	client *kgo.Client

	// made is the topics this connection already made sure of.
	made sync.Map
}

func (c *kafkaConn) publish(ctx context.Context, topic string, message []byte) error {
	c.ensure(ctx, c.client, topic)
	return c.client.ProduceSync(ctx, &kgo.Record{Topic: topic, Value: message}).FirstErr()
}

// ensure creates the topic with the cluster's defaults if it does not exist, so a
// consumer joining before the first publish has an end to start at. A refusal (no
// permission, say) is left for the publish or the fetch to report.
func (c *kafkaConn) ensure(ctx context.Context, client *kgo.Client, topic string) {
	if _, done := c.made.Load(topic); done {
		return
	}
	res, err := kadm.NewClient(client).CreateTopic(ctx, -1, -1, nil, topic)
	if err == nil && (res.Err == nil || errors.Is(res.Err, kerr.TopicAlreadyExists)) {
		c.made.Store(topic, struct{}{})
	}
}

func (c *kafkaConn) consume(ctx context.Context, topic, group string, to handler) error {
	client, err := kgo.NewClient(append(slices.Clone(c.opts),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()),
		kgo.AutoCommitMarks(),
		kgo.OnPartitionsRevoked(commitMarked),
	)...)
	if err != nil {
		return fmt.Errorf("stream: kafka: %w", err)
	}
	defer client.Close()
	defer commitMarked(context.Background(), client, nil)

	c.ensure(ctx, client, topic)
	for {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil || fetches.IsClientClosed() {
			return ctx.Err()
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			return fmt.Errorf("stream: kafka %s: %w", topic, errs[0].Err)
		}
		for iter := fetches.RecordIter(); !iter.Done(); {
			record := iter.Next()
			id := strconv.Itoa(int(record.Partition)) + "-" + strconv.FormatInt(record.Offset, 10)
			if !offer(ctx, to, record.Value, id) {
				return ctx.Err()
			}
			client.MarkCommitRecords(record)
		}
	}
}

// commitMarked commits what was delivered before partitions move to another node,
// so that node does not deliver it again.
func commitMarked(ctx context.Context, client *kgo.Client, _ map[string][]int32) {
	ctx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	if err := client.CommitMarkedOffsets(ctx); err != nil {
		logger.Warnf("stream: committing delivered offsets: %s", err)
	}
}

// offer hands a message to a subscriber until it takes it or maxAttempts pass. It
// reports false only when ctx ended first, leaving the message to be delivered again.
func offer(ctx context.Context, to handler, message []byte, id string) bool {
	for attempt := 1; ; attempt++ {
		err := to.handle(ctx, message, id)
		if err == nil {
			return true
		}
		if attempt == maxAttempts {
			logger.Warnf("stream: passing over %s after %d attempts: %s", id, attempt, err)
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff(attempt)):
		}
	}
}

func (c *kafkaConn) Close() error {
	c.client.Close()
	return nil
}
