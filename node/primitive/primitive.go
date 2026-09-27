// Package primitive is this node's side of services/primitives: the reach every
// caller here sends, appends and claims through, and what the service is told
// about the crons this node holds.
//
// # Why it is its own package
//
// Both halves are ADAPTERS. The first sits between everything on this node that
// touches the `sync` kind — a guest sending to a queue or appending to a log,
// the dispatcher claiming from one — and a reach that answers WHICH primitives
// service this node uses. The second sits between a worker that resolved some
// crons and a service that fires them, neither of which may import the other.
// The composition root is the only place that may NAME any of that — but naming
// them and implementing the adapter are different jobs, and conflating them is
// how node/ grew to eight thousand lines with twenty-two of these in one
// namespace.
//
// So the root hands each of these exactly what it can ask. Everything this
// package needs is in its own signature, which is the property a type taking
// *Node does not have: that one can reach anything the node has, so nothing
// about it says what it actually depends on, and it cannot be built anywhere
// except beside the node.
//
// # Its name is the service's and not the kind's
//
// The vocabulary's word for what the first half backs is `sync`, and a package
// called that would shadow the standard library in the root that has to import
// both. So it is named for the service whose two types it reaches.
//
// # What is not here
//
// WHICH ARM — this node's own service or one across the swarm — and which node
// a window of the clock is handed to. Both are read off the same stats view
// every other locator on this node reads, so they stay with the composition
// that has the view; what is here is what is asked once an arm has been chosen.
package primitive

import (
	"context"
	"errors"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	primitives "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// Seam is every surface this node reaches the primitives service through: the
// two the wasm tier asks for, and the claim half the dispatcher drives.
//
// IT IS ONE INTERFACE BECAUSE BOTH ARMS ANSWER ALL THREE. The service in this
// process and a stream to one across the swarm each satisfy it, which is what
// makes the choice between them invisible above this line.
type Seam interface {
	wasmtier.Queues
	wasmtier.Logs
	primitives.Consumers
}

// Reach answers which arm to use, now.
//
// ONE METHOD AND NOT p2p/rpc's Reacher, whose At and Close belong to whoever
// decided how the stream is kept and what closes it. What a send needs is the
// answer.
type Reach interface {
	Now(ctx context.Context) (Seam, error)
}

// Backing is the seam every caller on this node holds. Construct it with New.
//
// IT RESOLVES PER CALL rather than at construction, because a node that runs no
// service of its own may see no fleet yet when it is composed, and a service
// that appears later must still be found.
type Backing struct{ reach Reach }

var (
	_ wasmtier.Queues      = (*Backing)(nil)
	_ wasmtier.Logs        = (*Backing)(nil)
	_ primitives.Consumers = (*Backing)(nil)
)

// New wires the backing.
//
// reach MAY NOT BE NIL, because a backing with nothing to resolve fails every
// call — and the root has something to hand over even on a node that can reach
// no primitives service at all: that reach refuses, by name, on the first send.
func New(reach Reach) (*Backing, error) {
	if reach == nil {
		return nil, errors.New("primitive: no reach, so no service can be asked")
	}
	return &Backing{reach: reach}, nil
}

func (b *Backing) Send(ctx context.Context, grant invoke.Grant, id string, data []byte) error {
	seam, err := b.reach.Now(ctx)
	if err != nil {
		return err
	}
	return seam.Send(ctx, grant, id, data)
}

func (b *Backing) Claim(ctx context.Context, grant invoke.Grant, max int) ([]primitives.Delivery, error) {
	seam, err := b.reach.Now(ctx)
	if err != nil {
		return nil, err
	}
	return seam.Claim(ctx, grant, max)
}

func (b *Backing) Settle(ctx context.Context, grant invoke.Grant, delivery primitives.Delivery, done bool) error {
	seam, err := b.reach.Now(ctx)
	if err != nil {
		return err
	}
	return seam.Settle(ctx, grant, delivery, done)
}

func (b *Backing) Depth(ctx context.Context, grant invoke.Grant) (primitives.Depth, error) {
	seam, err := b.reach.Now(ctx)
	if err != nil {
		return primitives.Depth{}, err
	}
	return seam.Depth(ctx, grant)
}

func (b *Backing) Append(ctx context.Context, grant invoke.Grant, data []byte) (string, error) {
	seam, err := b.reach.Now(ctx)
	if err != nil {
		return "", err
	}
	return seam.Append(ctx, grant, data)
}

func (b *Backing) Read(ctx context.Context, grant invoke.Grant, after string, limit uint32) ([]wasmtier.Record, error) {
	seam, err := b.reach.Now(ctx)
	if err != nil {
		return nil, err
	}
	return seam.Read(ctx, grant, after, limit)
}

// Deployments is the worker's view of what this node is already serving.
//
// ONE METHOD AND NOT THE WORKER, because that one is the whole of what the
// service has to be told: which crons exist. A parameter typed as the worker
// would carry Invoke, Resolve, Transform, Route, Direct, Shape and Conn along
// with it, none of which a schedule has any business reaching.
type Deployments interface {
	Schedules(ctx context.Context) []worker.Schedule
}

// Schedules is every cron in the deployments a worker holds, in the shape the
// primitives service reads them.
//
// IT IS THE ONE PLACE THE TWO SHAPES MEET, which is what keeps a consensus
// service from importing a compute one: the composer holds both and neither
// holds the other.
//
// holds MAY BE NIL, and that is a node whose worker has not been built yet, or
// one that runs none: it answers none, and the service's pass over none does
// nothing. A node that has resolved nothing answers none the same way.
//
// EVERY FIELD CROSSES, and nothing checks that but a test. A schedule dropped
// here is an outage nobody sees — a cron that never fires — and a FIELD dropped
// here is worse: a nightly job whose timezone was lost fires at the right time
// in the wrong place, and every log line about it looks correct.
func Schedules(ctx context.Context, holds Deployments) []primitives.Schedule {
	if holds == nil {
		return nil
	}

	held := holds.Schedules(ctx)
	out := make([]primitives.Schedule, 0, len(held))
	for _, s := range held {
		fires := make([]primitives.Target, 0, len(s.Fires))
		for _, at := range s.Fires {
			fires = append(fires, primitives.Target{
				Project:     at.Project,
				Environment: at.Environment,
				Scope:       at.Scope,
				Kind:        at.Kind,
				Name:        at.Name,
			})
		}
		out = append(out, primitives.Schedule{
			Handle:    s.Handle,
			Name:      s.Name,
			ID:        s.ID,
			Path:      s.Path,
			Spec:      s.Spec,
			Timezone:  s.Timezone,
			OnOverlap: s.OnOverlap,
			Jitter:    s.Jitter,
			Fires:     fires,
			Starts:    s.Starts,
			Refused:   s.Refused,
		})
	}
	return out
}
