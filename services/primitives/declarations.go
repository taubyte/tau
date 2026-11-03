package primitives

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// A call carries a deployment and a resource id; everything about the primitive is
// read here from the compiled project. What is served is a table keyed by (kind,
// type), so a new sync type is one row: which resolutions it keeps, and whether the
// lifecycle loop walks it.

// serving is one row: what this service does with a (kind, type).
type serving struct {
	// consensus is the resolutions this service keeps for the type. A
	// resolution not listed is refused BY NAME rather than served weaker than
	// written.
	consensus []string

	// retained says the lifecycle loop walks resources of this type, a column because a
	// cache or a lock will want a different answer.
	retained bool
}

// served is what this service implements: both types raft-only, since every mechanism
// here (one head, one claim, one leader) is the opposite of crdt's convergent order.
var served = map[[2]string]serving{
	{syncKind, queueType}: {consensus: []string{consensusRaft}, retained: true},
	{syncKind, logType}:   {consensus: []string{consensusRaft}, retained: true},
}

// declared is one sync resource as its author wrote it, plus the deployment
// that wrote it.
type declared struct {
	spec     Spec
	revision uint64

	// delivery is a queue's own budget, with pkg/queue's defaults filled in and
	// its name and dead letter already scoped. Zero for a log.
	delivery queue.Config

	// keep is a log's retention, and for a queue how long an item may wait
	// unclaimed. Zero for a log whose author wrote none, which the vocabulary
	// spells as forever; a queue's is always defaulted.
	keep retention
}

// retention is a drop-oldest budget: what is kept, and for how long. Both are
// the log's pair and the vocabulary's own words; the dead letter reuses them
// verbatim rather than inventing a second spelling of the same idea.
type retention struct {
	age  time.Duration
	size int64
}

func (r retention) bounded() bool { return r.age > 0 || r.size > 0 }

// serves refuses a declaration this service cannot honour, naming which half. What
// this cloud serves is checked before what the caller asked.
func (d declared) serves(want string) error {
	row, held := served[[2]string{syncKind, d.spec.Type}]
	if !held {
		return fmt.Errorf("%w: %s", ErrTypeUnserved, d.spec.Type)
	}
	if !slices.Contains(row.consensus, d.spec.Consensus) {
		return fmt.Errorf("%w: %s asks for %q", ErrConsensusUnserved, d.spec.Type, d.spec.Consensus)
	}
	if want != "" && want != d.spec.Type {
		return fmt.Errorf("%w: %s is a %s", ErrWrongType, d.spec.Name, d.spec.Type)
	}
	return nil
}

type declarations struct {
	projects *register.Cache[*specs.Project]
}

func (d *declarations) of(ctx context.Context, ref address) (declared, error) {
	if err := ref.valid(); err != nil {
		return declared{}, err
	}

	project, pointer, err := d.projects.At(ctx, register.Deployment{
		Project: ref.project, Environment: ref.environment,
	})
	if err != nil {
		return declared{}, fmt.Errorf("primitives: reading %s/%s: %w", ref.project, ref.environment, err)
	}

	kind, scope, name, held := project.Locate(ref.id)
	if !held {
		return declared{}, fmt.Errorf("%w: %s", ErrNotDeclared, ref.id)
	}
	if kind != syncKind {
		return declared{}, fmt.Errorf("%w: %s is a %s", ErrNotASync, ref.id, kind)
	}

	resource, _, err := project.Resource(scope, kind, name)
	if err != nil {
		return declared{}, fmt.Errorf("%w: %s: %w", ErrNotDeclared, ref.id, err)
	}

	// The type is the compiled discriminator.
	primitive := specs.ReadSync(resource)
	kindOf := primitive.Type
	if kindOf == "" {
		return declared{}, fmt.Errorf("%w: %s declares no type", ErrNotASync, ref.id)
	}

	out := declared{
		revision: pointer.Revision,
		spec: Spec{
			Project:     ref.project,
			Environment: ref.environment,
			Type:        kindOf,

			// The id, never the label, and the id alone, so the lifecycle pass can resolve a place
			// back to this declaration (see Spec.at).
			Name: ref.id,

			// Resolved here, so nothing below decides what silence meant.
			Consensus: consensusOf(consensusIn(primitive)),
		},
	}

	switch kindOf {
	case queueType:
		out.delivery = deliveryOf(out.spec, primitive.Queue)
		out.keep = unclaimedOf(primitive.Queue)
	case logType:
		out.keep = keepOf(primitive.Log)
	}
	return out, nil
}

// consensusIn is the consensus the type's own block declares.
func consensusIn(primitive specs.Sync) string {
	if primitive.Type == logType {
		return primitive.Log.Consensus
	}
	return primitive.Queue.Consensus
}

// consensusOf is what the author wrote, or for a declaration that skipped the
// compiler's check, the promise this service keeps.
func consensusOf(written string) string {
	if written == "" {
		return consensusRaft
	}
	return written
}

// deliveryOf reads a queue's budget out of its own block.
//
// pkg/queue FILLS ITS OWN DEFAULTS and they are the vocabulary's — a 30s
// visibility timeout, five attempts — so an absent attribute is left absent
// here rather than being written twice in two places that can drift.
func deliveryOf(spec Spec, declared specs.SyncQueue) queue.Config {
	cfg := queue.Config{Name: spec.Prefix()}

	// A dead letter compiles to another queue's id, scoped as this queue is. Without one,
	// pkg/queue derives an undeclared keyspace, which retention.go bounds.
	if dead := declared.DeadLetterName; dead != "" {
		cfg.DeadLetter = Spec{
			Project: spec.Project, Environment: spec.Environment,
			Type: queueType, Name: dead,
		}.Prefix()
	}

	if d := declared.VisibilityTimeout; d != nil {
		cfg.VisibilityTimeout = *d
	}
	if d := declared.Delay; d != nil {
		cfg.Delay = *d
	}
	if d := declared.RetryInterval; d != nil {
		cfg.RetryInterval = *d
	}
	if d := declared.RetryCeiling; d != nil {
		cfg.RetryCeiling = *d
	}
	if n := declared.MaxAttempts; n != nil && *n > 0 {
		cfg.MaxAttempts = uint32(*n)
	}
	// THE QUEUE'S SIZE IS WHAT ITS ITEMS MAY HOLD; a send past it is refused.
	if n := declared.RetentionSize; n != nil && *n > 0 {
		cfg.MaxBytes = *n
	}
	// A COMPILED NUMBER IS A NUMBER. `retry/factor` is a Float in the
	// vocabulary; reading it as a string answers "" and the multiplier never
	// travels, so an author who wrote `factor: 2` gets a flat interval with
	// nothing saying so.
	if f := declared.RetryFactor; f != nil {
		cfg.RetryFactor = *f
	}
	return cfg
}

// unclaimedOf reads a queue's retention age: how long an item may wait unclaimed
// before the lifecycle pass moves it to the dead letter. Its size is the queue's
// own bound (deliveryOf).
func unclaimedOf(declared specs.SyncQueue) retention {
	var out retention
	if d := declared.RetentionAge; d != nil {
		out.age = *d
	}
	return out
}

// keepOf reads a log's retention.
//
// ABSENT MEANS FOREVER, which is the vocabulary's own sentence and the safe
// direction for a ledger — and the one place this service does not get to
// improve on it. What forever costs is in retention.go.
func keepOf(declared specs.SyncLog) retention {
	var out retention
	if d := declared.RetentionAge; d != nil {
		out.age = *d
	}
	if n := declared.RetentionSize; n != nil {
		out.size = *n
	}
	return out
}
