// Package primitive is this node's side of services/primitives: the reach every
// caller sends, appends and claims through, and the crons reported to the service.
// Named for the service, since `sync` would shadow the standard library. Which arm
// (local service or remote) and where a clock window goes are the composition's.
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

// Schedules is every cron in the deployments a worker holds, in the primitives
// service's shape, the one place the two shapes meet. holds may be nil (no worker
// yet). Every field must cross: a lost timezone fires a nightly job at the right time
// in the wrong place.
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
