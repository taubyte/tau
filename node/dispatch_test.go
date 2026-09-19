package node

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/stats"
	"github.com/taubyte/tau/services/v2/placement"
)

// dispatch_test drives the claim loop synchronously, which is the whole reason
// step and loop are separate: a background goroutine reached only through a real
// clock has branches nothing can get to, and every branch here is one that
// decides whether somebody's work is lost.

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
		"source":    map[string]any{"kind": "component", "component": "QmS4ustL54uo8FzR9455qaxZwuMiUhyvMcX9Ba8nUH4uVv"},
		"sync":      map[string]any{"jobs": entry},
	}
	if c.Concurrency > 0 {
		workload["limits"] = map[string]any{"concurrency": float64(c.Concurrency)}
	}

	// The visibility timeout is a minute so nothing expires mid-test: what a
	// test drives is the DELIVERY, and an expiry racing it would make the
	// counts depend on how long a step took.
	//	THE FIELDS SIT UNDER THE TYPE'S BLOCK, because `sync` is a typed kind and
	//	the block that is written IS the type — the same shape a database's `sql`
	//	block has.
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
			"id":   projectID,
			"name": "shop",
			"function": map[string]any{
				"QmSgvGwXHLPnCXKTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPY": workload,
			},
			"sync": map[string]any{queueID: target},
		},
		"indexes": map[string]any{
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
func depth(t *testing.T, n *Node) (ready, claimed int) {
	t.Helper()
	ready, claimed, ok := n.QueueDepth("shop/prod/" + queueID)
	require.True(t, ok, "the queue was never opened")
	return ready, claimed
}

// consuming is a node with a worker, a queue and a tier that answers.
func consuming(t *testing.T, answers *answering, compiled []byte) (*Node, *dispatcher) {
	t.Helper()

	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers))
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

	// Start forms the queue cluster in the background; the mock one is
	// instant, so waiting for it is what makes a step deterministic.
	<-n.work.forming

	return n, &dispatcher{node: n}
}

// push puts one item on the queue the fixture declares.
//
// IT USES THE CONSUMER'S OWN GRANT, which is what a producing workload would
// send with — a grant assembled here would carry no settings, and the queue
// would be opened with pkg/queue's defaults rather than what the author wrote.
func push(t *testing.T, n *Node, id string, data []byte) {
	t.Helper()

	consumers := n.Worker().Consumers(t.Context())
	require.NotEmpty(t, consumers, "the fixture declares a consumer")
	require.NoError(t, n.work.Send(t.Context(), consumers[0].Queue, id, data))
}

// TestAnItemPushedIsAnItemDelivered — the whole point of the file: something
// claims, and the workload that declared `receive` is invoked with the payload.
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

// TestAConsumerIsToldWhichQueueAndWhichItem. A workload consuming two queues is
// dispatched at one path and has nothing else to tell them apart by.
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

// TestTheDeclaredPathIsWhereItIsPosted. A container answers on a path it chose;
// a workload that declared none is called at the root.
func TestAnItemIsPostedWhereTheEntrySaid(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 5, Path: "/work"}))

	push(t, n, "job-1", []byte("x"))
	require.Equal(t, 1, d.step(t.Context()))
	require.Equal(t, "/work", answers.seen()[0].Path)
}

// TestAFailedDeliveryIsLEFTCLAIMED when the author declared no retry interval.
//
// The vocabulary says an absent `retry.interval` means "the visibility timeout
// alone governs", and this is where that sentence is true or is not: a nack
// gives the item back NOW, so with no interval it would land ready inside the
// same cycle and a consumer failing in a millisecond would spend every attempt
// the author allowed in a millisecond.
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

// TestAFailedDeliveryComesBack when there IS an interval to space it by, and
// comes back with its attempt count moved on — which is what makes a consumer
// that crashed a retry rather than a loss.
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

// TestAnItemThatExhaustsItsAttemptsIsNotRetriedForever. The author wrote how
// many deliveries an item gets; after them pkg/queue moves it aside rather than
// handing it round again.
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

// TestATierThatFailsIsAFailedDelivery, and not a swallowed one: a workload that
// cannot even be started must not consume the item that would have been given
// to it.
func TestAnInvocationThatErrorsIsNotAnAcknowledgement(t *testing.T) {
	answers := &answering{name: "wasm", fail: true}
	n, d := consuming(t, answers, withConsumer(consumer{Attempts: 5, RetryEvery: time.Hour}))

	push(t, n, "job-1", []byte("x"))
	require.Equal(t, 1, d.step(t.Context()))

	ready, _ := depth(t, n)
	require.Equal(t, 1, ready, "the item is still there, waiting out its backoff")
}

// TestANodeDrainsOnlyWhatItHolds. The register does not enumerate, so what a
// node drains is what it is already serving — and a published deployment
// nothing has resolved is not that.
func TestANodeDrainsOnlyWhatItHolds(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers))
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

// TestAProjectWithNoConsumerDrainsNothing, so a node holding an ordinary
// deployment does no work per cycle.
func TestAProjectThatConsumesNothingIsNotWalkedForItems(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, compiled())

	require.Empty(t, n.Worker().Consumers(t.Context()))
	require.Equal(t, 0, d.step(t.Context()))
}

// TestAsManyAtOnceAsTheWorkloadSaidItMayHold. `limits/concurrency` is the
// vocabulary's own number for how many items an instance may hold, which is
// only meaningful if the platform is the one pushing — so this is where that
// sentence becomes true.
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

// TestAWorkloadThatDeclaredNoConcurrencyGetsOneAtATime, which is the answer
// that cannot surprise anybody.
func TestNoDeclaredConcurrencyIsOneAtATime(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, _ := consuming(t, answers, withConsumer(consumer{Attempts: 5}))

	require.Zero(t, n.Worker().Consumers(t.Context())[0].Concurrency)
}

// TestTheLoopStopsWithTheNode, so a claim is never in flight against a store
// that is being torn down.
func TestTheClaimLoopStopsWhenTheNodeCloses(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, _, _ := makeNode(t, Config{Services: []string{Register, Worker, Stats}},
		WithRuntimes(answers))
	require.NoError(t, n.Start(t.Context()))

	require.NotNil(t, n.dispatchDone, "a node with a worker and raft runs one")
	require.NoError(t, n.Close())

	select {
	case <-n.dispatchDone:
	default:
		t.Fatal("Close returned with the claim loop still running")
	}
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

func (nowhere) Route(string, time.Duration) []stats.Node { return nil }

// TestADELIVERYISPLACEDANDNOTRUNWHEREITWASCLAIMED.
//
// A claim is leader-only — pkg/queue refuses a write off the leader, which is
// what makes it exclusive without a second election. So a dispatcher that also
// RAN what it claimed put every step of every workflow in the cloud on one
// node, with the rest of the fleet idle: the queue was distributed and the work
// was not. Every other path — a gateway request, a proxy connection, a channel
// dial — already placed the same workloads.
//
// IT IS TESTED BY TAKING THE CANDIDATES AWAY. With one node, placement keeps
// the local worker as a candidate and chooses it, so a passing delivery cannot
// tell the two apart. A placement that can reach nobody can: the delivery has
// to fail, and on the old code it would have quietly succeeded locally.
func TestADeliveryIsPlacedRatherThanRunWhereItWasClaimed(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, withConsumer(consumer{}))

	placed, err := placement.New(n.opts.host, nowhere{})
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

// TestANODEWITHNOPLACEMENTSTILLRUNSWHATITHOLDS. openPlacement skips a node
// that runs no stats, and says why: such a node "can only reach itself". It
// must still drain the queues it holds rather than refuse work for want of a
// fleet to spread it over.
func TestANodeThatCanOnlyReachItselfStillRunsWhatItHolds(t *testing.T) {
	answers := &answering{name: "wasm", status: 200}
	n, d := consuming(t, answers, withConsumer(consumer{}))
	n.workers = nil

	push(t, n, "1", []byte("hello"))
	require.Equal(t, 1, d.step(t.Context()))
	require.NotEmpty(t, answers.seen(), "a node with no placement ran it locally")
}
