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

// declarations.go turns "which resource" into "what that resource is".
//
// # The caller does not get a say
//
// A call carries a deployment and a resource id and nothing else. Everything
// about the primitive — its type, how concurrent writes resolve, how long a
// claim is invisible for, how many attempts it gets, where an exhausted item
// goes, what retention keeps — is read HERE, out of the compiled project.
// node/queue.go read all of it off the GRANT, which is the caller's side of the
// conversation: a worker that described the queue could describe a different
// one.
//
// # The table, and what it is for
//
// What this service serves is a TABLE keyed by (kind, type) rather than a
// switch inlined in the resolve path. The vocabulary's own shape says why: a
// lock, a counter and a barrier are each one more row in `syncTypes`, and a
// cache is a kind of its own with the same question to answer. A row carries
// what differs — which resolutions it keeps, and whether the lifecycle loop
// walks it — so growing this service is an entry rather than a sweep.

// serving is one row: what this service does with a (kind, type).
type serving struct {
	// consensus is the resolutions this service keeps for the type. A
	// resolution not listed is refused BY NAME rather than served weaker than
	// written.
	consensus []string

	// retained says the lifecycle loop walks resources of this type. It is a
	// COLUMN rather than something the loop assumes, because the next type will
	// not want the same answer: a cache wants eviction on its own terms and a
	// lock wants none at all, and a loop that assumed would have to be edited
	// to add one rather than a row.
	retained bool
}

// served is what this service implements today.
//
// BOTH TYPES ARE RAFT-ONLY, and that is a promise rather than a limitation.
// `crdt` is append-anywhere with an order that converges; every mechanism below
// — one head, one claim, one leader — is the other answer, and serving a crdt
// declaration on it would answer an author's question with the wrong one.
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

	// keep is a log's retention. Zero for a queue, and zero for a log whose
	// author wrote none — which the vocabulary spells as forever.
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

// serves refuses a declaration this service cannot honour, naming which half of
// it is the problem.
//
// WHAT THIS CLOUD SERVES, BEFORE WHAT THE CALLER ASKED FOR. A lock reached
// through QueueSend is two different mistakes depending on the order these are
// checked, and the one worth reporting is that this service does not serve a
// lock — not that the caller used the wrong verb on a resource it could never
// have used the right one on.
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

// declarations reads compiled projects.
type declarations struct {
	projects *register.Cache[*specs.Project]
}

// of resolves one sync resource.
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

	// THE TYPE IS THE DISCRIMINATOR THE COMPILER WROTE. `queue: {…}` compiles to
	// a `type` of "queue", which is how a reader knows which of the two
	// operation surfaces this resource has without guessing from which block is
	// present.
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

			// THE ID AND NEVER THE LABEL, for the reason node/queue.go already
			// gives: two workloads may call one queue different things, and two
			// projects may both call one `jobs`. And the id ALONE, which is
			// what lets the lifecycle pass resolve a place back to this
			// declaration — see [Spec.at].
			Name: ref.id,

			// RESOLVED HERE AND NOWHERE LATER, so nothing below has to decide
			// what silence meant. `consensus` is RequiredWhen the type is
			// written, so a hole in it is a resource the compiler let through.
			Consensus: consensusOf(consensusIn(primitive)),
		},
	}

	switch kindOf {
	case queueType:
		out.delivery = deliveryOf(out.spec, primitive.Queue)
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

// consensusOf is what the author wrote, or what a resource that wrote nothing
// gets. `consensus` is RequiredWhen the type is written, so this is the arm for
// a declaration that reached a runtime without passing the compiler's check —
// and it defaults to the promise this service can actually keep.
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

	// A DEAD LETTER IS A REFERENCE TO ANOTHER QUEUE, and the compiled value is
	// that queue's id. It is scoped exactly as this queue is, so it lands beside
	// the one it drains and two projects' dead letters never meet. An absent one
	// leaves pkg/queue to derive a keyspace from this queue's name — which
	// nothing declares and therefore nothing can drain, which is why
	// retention.go bounds it.
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
	// A COMPILED NUMBER IS A NUMBER. `retry/factor` is a Float in the
	// vocabulary; reading it as a string answers "" and the multiplier never
	// travels, so an author who wrote `factor: 2` gets a flat interval with
	// nothing saying so.
	if f := declared.RetryFactor; f != nil {
		cfg.RetryFactor = *f
	}
	return cfg
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
