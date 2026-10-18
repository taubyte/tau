package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// queue.go is the host half of `taubyte:queue/producer` — the `sync` kind at
// `type: queue`.
//
// THE KIND IS `sync` AND THE TYPE SELECTS THIS INTERFACE, the same way `database`
// at `type: sql` selects sql.go's. A grant is filtered by kind AND type here for
// exactly that reason: `sync` also covers a log, whose verbs are not these and
// whose backing is a different thing, and a filter on kind alone would hand a
// log's grant to the producer.
//
// # Only send, and that is a different absence from stream's
//
// `stream.subscribe` is not served because its semantics are UNSETTLED — a
// pubsub message reaching every node that runs the subscriber would invoke it
// once per node, and which of fan-out and exactly-once is meant is a product
// decision. A queue has no such question: a claim is exclusive and leader-only,
// so exactly one consumer gets an item and an unacknowledged one comes back.
// What `receive` needs is a DISPATCHER — something that claims and invokes —
// and not a function a guest calls, which is why there is none here.
//
// # not-writable is its own arm
//
// pkg/queue refuses a write off the raft leader and deliberately does not
// forward. A caller RETRIES that and nothing about the request was wrong, which
// is a different instruction from "the store is unreachable" and a very
// different one from "you may not".

const queueInterface = "taubyte:queue/producer@0.1.0"

const (
	opSend    = "send"
	opReceive = "receive"
)

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchQueue uint32 = iota
	errQueueDenied
	errNotWritable
	errQueueUnavailable
)

var (
	// ErrNoQueues is a spec that grants a queue on a tier composed without a
	// backing.
	ErrNoQueues = errors.New("wasm: this resource sends to a queue and this tier has nothing to send with")

	// ErrNotWritable is a node that is not the one that may write. It is the
	// tier's spelling of raft's ErrNotLeader, and a caller retries it.
	ErrNotWritable = errors.New("wasm: this node may not write to that queue")
)

const (
	// syncKind is the vocabulary's kind; queueType is the type under it that this
	// interface serves.
	syncKind  = specs.KindSync
	queueType = specs.SyncTypeQueue

	// settingSyncType is where the composer writes which primitive a grant names.
	settingSyncType = specs.SyncKeyType
)

// Queues sends to the queue a grant names.
type Queues interface {
	Send(ctx context.Context, grant invoke.Grant, id string, data []byte) error
}

// WithQueues is what backs `taubyte:queue/producer`.
func WithQueues(q Queues) Option {
	return func(r *Runtime) { r.queues = q }
}

func queueGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != syncKind || g.Settings[settingSyncType] != queueType {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// queueImports registers send for one instantiation.
func (r *Runtime) queueImports(spec invoke.Spec) []component.Option {
	grants := queueGrantsOf(spec)

	table := component.NewTypeTable()
	failureRef := table.Variant(
		component.VariantCaseSpec{Name: "no-such-queue", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "not-writable", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
	)
	descriptor := table.Func(
		[]component.TypeRef{
			component.Prim("string"), component.Prim("string"),
			table.List(component.Prim("u8")),
		},
		table.Result(component.TypeRef{}, failureRef),
	)

	send := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("queue.send: expected 3 arguments, got %d", len(args))
		}
		label, id, err := twoStrings(args)
		if err != nil {
			return nil, fmt.Errorf("queue.send: %w", err)
		}
		data, err := readBytes(args[2])
		if err != nil {
			return nil, fmt.Errorf("queue.send: %w", err)
		}

		grant, ok := grants[label]
		if !ok {
			return errorResult(queueFailure(errNoSuchQueue, label)), nil
		}
		if grant.Refused != "" {
			return errorResult(queueFailure(errQueueDenied, grant.Refused)), nil
		}
		if !grant.Allows(opSend) {
			return errorResult(queueFailure(errQueueDenied, label+" cannot send")), nil
		}
		if r.queues == nil {
			return errorResult(queueFailure(errQueueUnavailable, "this node has nothing to send with")), nil
		}

		if err := r.queues.Send(ctx, grant, id, data); err != nil {
			return errorResult(queueArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	return []component.Option{
		component.WithImportCustom(queueInterface, opSend, send, descriptor, table.Resolver()),
	}
}

func queueArm(err error) *component.VariantValue {
	if errors.Is(err, ErrNotWritable) {
		return queueFailure(errNotWritable, err.Error())
	}
	return queueFailure(errQueueUnavailable, err.Error())
}

func queueFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
