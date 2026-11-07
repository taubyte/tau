package instance

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance/placement"
	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// Drives the claim loop synchronously; every branch here decides whether work is
// lost.

const queueID = "QmXnnyufdzAWL5CqZ2RnSNgPbvCc1ALT73s6epPrRnZ1Zq"

// consumer is the fixture's knobs: everything a queue's delivery block can say
// that changes what the dispatcher does.
type consumer struct {
	// Path is the `deliver/path` on the access entry. Empty means the entry
	// declares none, which is the WASM shape.
	Path string

	// Attempts is `max_attempts` — small in a test so an item reaches the end
	// of its rope in a cycle or two rather than in five.
	Attempts int

	// RetryEvery is `retry/interval`. ZERO IS THE INTERESTING CASE: the
	// vocabulary says an absent one means the visibility timeout governs, and
	// that is the branch where the dispatcher must not nack.
	RetryEvery time.Duration

	// Concurrency is `limits/concurrency` on the workload.
	Concurrency int
}

// withConsumer is a project whose one function receives from one queue.
func withConsumer(c consumer) []byte {
	entry := map[string]any{"ops": []any{"receive"}}
	if c.Path != "" {
		entry["deliver"] = map[string]any{"path": c.Path}
	}

	workload := map[string]any{
		"name":      "checkout",
		"execution": map[string]any{"call": "handle", "memory": 33554432},
		"package":   map[string]any{"name": codePackageID},
		"sync":      map[string]any{"jobs": entry},
	}
	if c.Concurrency > 0 {
		workload["limits"] = map[string]any{"concurrency": float64(c.Concurrency)}
	}

	// A one-minute visibility timeout so nothing expires mid-test. The fields sit under
	// the type's block, as `sync` is a typed kind.
	queue := map[string]any{
		"consensus":          "raft",
		"visibility_timeout": float64(time.Minute),
		"max_attempts":       float64(c.Attempts),
	}
	if c.RetryEvery > 0 {
		queue["retry"] = map[string]any{"interval": float64(c.RetryEvery)}
	}
	target := map[string]any{"name": "jobs", "type": "queue", "queue": queue}

	doc := map[string]any{
		"object": map[string]any{
			"id":      projectID,
			"name":    "shop",
			"package": map[string]any{codePackageID: map[string]any{"name": "code", "type": "wasm"}},
			"function": map[string]any{
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": workload,
			},
			"sync": map[string]any{queueID: target},
		},
		"indexes": map[string]any{
			"projects/" + projectID + "/package/code/links": []any{
				"branches/main/projects/" + projectID + "/package/" + codePackageID},
			"projects/" + projectID + "/function/checkout/links": []any{
				"branches/main/projects/" + projectID + "/function/QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY"},
			"projects/" + projectID + "/sync/jobs/links": []any{
				"branches/main/projects/" + projectID + "/sync/" + queueID},
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

// depth is what the fixture's queue holds.
func depth(t *testing.T, n *Instance) (ready, claimed int) {
	t.Helper()
	ready, claimed, ok := n.QueueDepth("shop/prod/" + queueID)
	require.True(t, ok, "the queue was never opened")
	return ready, claimed
}

// consuming is a node with a worker, a queue and a tier that answers.
func consuming(t *testing.T, answers *answering, compiled []byte) (*Instance, *dispatcher) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats, Primitives}},
		WithRuntimes(answers), prebuilt())
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: compiled,
	})
	require.NoError(t, err)

	// The dispatcher drains what the worker HOLDS, and a project is held once
	// something has resolved it — which is the enumeration rule, stated in
	// services/worker/consume.go and pinned by TestANodeDrainsOnlyWhatItHolds.
	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)

	// Start forms the primitives service's group in the background; the mock
	// one is instant, so waiting for it is what makes a step deterministic.
	require.NoError(t, n.Primitives().WaitForLeader(t.Context()))

	// AND A DELIVERY IS PLACED, so this node has to be in its own view before it
	// can take its own work. Asking placement while the first stats report is
	// still in flight answers "no worker is ready to take this" — which is a
	// property of how fast the machine is, and nothing this file is about.
	sees(t, n, Worker)

	return n, &dispatcher{node: n}
}

// push puts one item on the fixture's queue with the consumer's own grant, so the
// author's settings apply.
func push(t *testing.T, n *Instance, id string, data []byte) {
	t.Helper()

	consumers := n.Worker().Consumers(t.Context())
	require.NotEmpty(t, consumers, "the fixture declares a consumer")
	require.NoError(t, n.syncs.Send(t.Context(), consumers[0].Queue, id, data))
}

func TestAnItemPushedReachesTheConsumer(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 5}))

	push(t, n, "job-1", []byte("do the thing"))
	require.Equal(t, 1, d.step(t.Context()))

	seen := answers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, "POST", seen[0].Method)
	require.Equal(t, "do the thing", string(seen[0].Body))

	// AND IT IS GONE, because a 2xx is an acknowledgement. A second cycle with
	// nothing to claim delivers nothing.
	require.Equal(t, 0, d.step(t.Context()))
}

func TestTheDeliveryNamesItself(t *testing.T) {
	answers := &answering{name: "wasm", status: 204}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 5}))

	push(t, n, "job-7", []byte("x"))
	require.Equal(t, 1, d.step(t.Context()))

	req := answers.seen()[0]
	require.Equal(t, []string{"jobs"}, req.Headers[headerQueue], "the LABEL the author wrote")
	require.Equal(t, []string{"job-7"}, req.Headers[headerDelivery])
	require.Equal(t, []string{"1"}, req.Headers[headerAttempt], "counting from one")
}

func TestAnItemIsPostedWhereTheEntrySaid(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 5, Path: "/work"}))

	push(t, n, "job-1", []byte("x"))
	require.Equal(t, 1, d.step(t.Context()))
	require.Equal(t, "/work", answers.seen()[0].Path)
}

func TestAFailureWithNoIntervalWaitsOutTheVisibilityTimeout(t *testing.T) {
	answers := &answering{name: "wasm", status: 500}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 5}))

	push(t, n, "job-1", []byte("x"))
	require.Equal(t, 1, d.step(t.Context()))
	require.Equal(t, 0, d.step(t.Context()), "and it is not handed round again in the meantime")

	require.Len(t, answers.seen(), 1)
	ready, claimed := depth(t, n)
	require.Zero(t, ready)
	require.Equal(t, 1, claimed, "still held, waiting out the timeout its author asked for")
}

func TestAConsumerThatRefusesGetsTheItemAgain(t *testing.T) {
	answers := &answering{name: "wasm", status: 500}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 3, RetryEvery: time.Nanosecond}))

	push(t, n, "job-1", []byte("x"))
	require.Equal(t, 3, d.step(t.Context()), "an interval of a nanosecond is an author saying 'now'")

	seen := answers.seen()
	require.Len(t, seen, 3)
	require.Equal(t, []string{"1"}, seen[0].Headers[headerAttempt])
	require.Equal(t, []string{"3"}, seen[2].Headers[headerAttempt], "the consumer can see it is a retry")
}

func TestAnExhaustedItemStopsBeingDelivered(t *testing.T) {
	answers := &answering{name: "wasm", status: 500}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 2, RetryEvery: time.Nanosecond}))

	push(t, n, "job-1", []byte("x"))
	for range 4 {
		d.step(t.Context())
	}
	require.Len(t, answers.seen(), 2, "two attempts is what the config said")

	ready, claimed := depth(t, n)
	require.Zero(t, ready)
	require.Zero(t, claimed)
}

func TestAnInvocationThatErrorsIsNotAnAcknowledgement(t *testing.T) {
	answers := &answering{name: "wasm", fail: true}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 5, RetryEvery: time.Hour}))

	push(t, n, "job-1", []byte("x"))
	require.Equal(t, 1, d.step(t.Context()))

	ready, _ := depth(t, n)
	require.Equal(t, 1, ready, "the item is still there, waiting out its backoff")
}

func TestANodeDrainsOnlyWhatItHolds(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers), prebuilt())
	require.NoError(t, n.Start(t.Context()))

	_, err := n.Register().Local().Publish(t.Context(), pkgregister.Publication{
		Project: "shop", Environment: "prod", Revision: 1, Compiled: withConsumer(consumer{Attempts: 5}),
	})
	require.NoError(t, err)

	require.Empty(t, n.Worker().Consumers(t.Context()), "nothing has asked for this deployment yet")

	_, err = n.Worker().Resolve(t.Context(), workerTarget())
	require.NoError(t, err)
	require.Len(t, n.Worker().Consumers(t.Context()), 1, "and now the node holds it")
}

func TestAProjectThatConsumesNothingIsNotWalkedForItems(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, compiled())

	require.Empty(t, n.Worker().Consumers(t.Context()))
	require.Equal(t, 0, d.step(t.Context()))
}

func TestConcurrencyIsTheWorkloadsOwnNumber(t *testing.T) {
	answers := &answering{name: "wasm", status: 200, block: make(chan struct{})}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 5, Concurrency: 8}))

	require.Equal(t, 8, n.Worker().Consumers(t.Context())[0].Concurrency)

	for i := range 20 {
		push(t, n, "job-"+strconv.Itoa(i), []byte("x"))
	}

	// EIGHT AT ONCE, WHICH IS THE ASSERTION. Counting only the twenty that came
	// out passes with the number ignored entirely — one goroutine draining
	// serially delivers twenty too — so what is watched is how many are inside
	// the workload while the cycle runs.
	drained := make(chan int, 1)
	go func() { drained <- d.step(t.Context()) }()

	require.Eventually(t, func() bool { return answers.inflight() == 8 },
		5*time.Second, 10*time.Millisecond, "the workload was not given eight at once")
	close(answers.block)

	require.Equal(t, 20, <-drained, "one cycle drains what is there")
}

func TestNoDeclaredConcurrencyIsOneAtATime(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, _ := consuming(t, answers, withConsumer(consumer{Attempts: 5}))

	require.Zero(t, n.Worker().Consumers(t.Context())[0].Concurrency)
}

func TestTheClaimLoopStopsWhenTheNodeCloses(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers), prebuilt())
	require.NoError(t, n.Start(t.Context()))

	require.NotNil(t, n.dispatchDone, "a node with a worker and raft runs one")
	require.NoError(t, n.Close())

	select {
	case <-n.dispatchDone:
	default:
		t.Fatal("Close returned with the claim loop still running")
	}
}

func TestAWorkerWithNoRaftStillRunsTheClaimLoop(t *testing.T) {
	// BUILT WITHOUT THE HARNESS, which always hands a node a raft opener: what
	// is under test is the composition that has none.
	n, err := New(mockPeer(t), rooted(t, Config{Services: []string{Worker, Stats}}), withKVDB(&memFactory{}), WithRuntimes(&answering{name: "wasm", status: 200}))
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	require.NoError(t, n.Start(t.Context()))

	require.NotNil(t, n.dispatchDone,
		"a compute node reaches a primitives service in the fleet, so it has queues to drain")
}

// errNoAnswer is a tier that could not start an instance at all — the failure a
// dispatcher must not treat as a consumed item.
var errNoAnswer = errors.New("this tier answers nothing")

// answering is a tier whose instances reply with what a test set.
type answering struct {
	name string

	// block, when set, holds every invocation open until it is closed — which
	// is how a test makes a run still be in flight when the clock comes round.
	block chan struct{}

	mu     sync.Mutex
	status int
	fail   bool
	reqs   []*invoke.Request
	inside int
}

func (a *answering) Name() string { return a.name }

func (a *answering) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return &answeringInstance{tier: a}, nil
}

func (a *answering) seen() []*invoke.Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*invoke.Request(nil), a.reqs...)
}

// inflight is how many invocations are inside Serve right now.
func (a *answering) inflight() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inside
}

type answeringInstance struct{ tier *answering }

func (i *answeringInstance) Serve(ctx context.Context, req *invoke.Request) (*invoke.Response, error) {
	i.tier.mu.Lock()
	i.tier.reqs = append(i.tier.reqs, req)
	i.tier.inside++
	status, fail, block := i.tier.status, i.tier.fail, i.tier.block
	i.tier.mu.Unlock()

	defer func() {
		i.tier.mu.Lock()
		i.tier.inside--
		i.tier.mu.Unlock()
	}()

	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail {
		return nil, errNoAnswer
	}
	return &invoke.Response{Status: status, Body: []byte(strconv.Itoa(status))}, nil
}

func (i *answeringInstance) Close() error { return nil }

// nowhere is a router that reports no node holding anything, so placement has
// no candidate to choose.
type nowhere struct{}

func (nowhere) RoutePeers(_ string, _ time.Duration, dst []string) []string { return dst }

func TestADeliveryIsPlacedRatherThanRunWhereItWasClaimed(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, withConsumer(consumer{}))

	placed, err := placement.New(n.Host(), nowhere{})
	require.NoError(t, err)
	n.workers = placed

	push(t, n, "1", []byte("hello"))
	require.Equal(t, 1, d.step(t.Context()), "the item was claimed")

	require.Empty(t, answers.seen(), "the workload ran where the claim was made")

	// AND THE ITEM IS NOT LOST. A placement that reached nobody is a failed
	// delivery, so the queue keeps it for the budget its author wrote.
	ready, claimed := depth(t, n)
	require.Equal(t, 1, ready+claimed, "the item is still the queue's")
}

func TestANodeThatCanOnlyReachItselfStillRunsWhatItHolds(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, withConsumer(consumer{}))
	n.workers = nil

	push(t, n, "1", []byte("hello"))
	require.Equal(t, 1, d.step(t.Context()))
	require.NotEmpty(t, answers.seen(), "a node with no placement ran it locally")
}
