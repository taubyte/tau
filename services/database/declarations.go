package database

import (
	"context"
	"fmt"
	"slices"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// declarations.go turns "which database" into "what that database is".
//
// # The caller does not get a say
//
// A call carries a deployment and a resource id and nothing else. Everything
// about the database — its model, its engine, where it lives, what it is
// allowed to grow to, how concurrent writes resolve — is read HERE, out of the
// compiled project. That is the difference between a service and a proxy: a
// caller that described the database could describe a different one, and the
// service would have no way to tell.
//
// # It is also where size and consensus stop being decoration
//
// Both are RequiredWhen the engine is distributed, and today neither reaches a
// runtime: services/worker carries engine, host, database, tls, dimensions and
// metric onto the grant, and nothing carries the other two. Reading the
// declaration rather than the grant reads all of it, which is why they appear
// below and did not appear there.

// declared is one database as its author wrote it, plus the deployment that
// wrote it — the revision is what tells a warm engine it has been replaced.
type declared struct {
	spec     database.Spec
	revision uint64

	// size is the ceiling the author declared, in bytes. Zero means none was
	// written, which the vocabulary only allows for a connector.
	size int64

	// consensus is how concurrent writes resolve, as the author wrote it.
	consensus string
}

// tauEngine is what the vocabulary calls tau's own, on both consensus arms.
const tauEngine = "distributed"

// served is what this service implements today: tau's own kv over the
// replicated CRDT store.
//
// THE LIST IS HERE AND NOT SPREAD THROUGH THE HANDLERS, so growing it is one
// edit and a reader can see the whole of what this cloud answers. `database`
// has four models on one axis and a consensus on another, and this is one cell
// of that grid — which is a thing to STATE, because a service that answered
// every call it was given and quietly did the wrong thing for three quarters of
// them is worse than one that refuses.
var (
	servedModels = []database.Type{database.KV}

	// servedConsensus maps what an author wrote to the engine that keeps it.
	//
	// THE VOCABULARY'S `engine` IS `distributed` FOR BOTH, because from where
	// the author sits the difference is not which engine — it is how concurrent
	// writes resolve. pkg/database's registry is keyed by (type, engine), so the
	// resolution becomes the engine name INSIDE this service and nowhere else:
	// the spec that leaves here carries it, the declaration that arrived did
	// not, and a reader of the vocabulary sees one engine with two behaviours
	// rather than two engines.
	servedConsensus = map[string]string{
		"crdt": "distributed-crdt",
		"raft": "distributed-raft",
	}

	// defaultConsensus is what a distributed database with none written gets.
	// The vocabulary makes it RequiredWhen distributed, so this is for a
	// connector and for a document that predates the field.
	defaultConsensus = "crdt"
)

// serves refuses a declaration this service cannot honour, naming which half of
// it is the problem.
func (d declared) serves() error {
	if !slices.Contains(servedModels, d.spec.Type) {
		return fmt.Errorf("%w: %s", ErrModelUnserved, d.spec.Type)
	}
	if _, known := servedConsensus[d.wants()]; !known {
		return fmt.Errorf("%w: %q", ErrConsensusUnserved, d.consensus)
	}
	return nil
}

// wants is the resolution this database asked for. Absent is the default and
// not a refusal: `consensus` is RequiredWhen the engine is distributed, so a
// connector carries none and refusing every database that did not write one
// would refuse every external postgres.
func (d declared) wants() string {
	if d.consensus == "" {
		return defaultConsensus
	}
	return d.consensus
}

// declarations reads compiled projects.
type declarations struct {
	projects *register.Cache[*specs.Project]
}

// of resolves one database.
func (d *declarations) of(ctx context.Context, ref address) (declared, error) {
	if err := ref.valid(); err != nil {
		return declared{}, err
	}

	project, pointer, err := d.projects.At(ctx, register.Deployment{
		Project: ref.project, Environment: ref.environment,
	})
	if err != nil {
		return declared{}, fmt.Errorf("database: reading %s/%s: %w", ref.project, ref.environment, err)
	}

	kind, scope, name, held := project.Locate(ref.id)
	if !held {
		return declared{}, fmt.Errorf("%w: %s", ErrNotDeclared, ref.id)
	}
	if kind != "database" {
		return declared{}, fmt.Errorf("%w: %s is a %s", ErrNotADatabase, ref.id, kind)
	}

	resource, _, err := project.Resource(scope, kind, name)
	if err != nil {
		return declared{}, fmt.Errorf("%w: %s: %w", ErrNotDeclared, ref.id, err)
	}

	// THE MODEL IS THE DISCRIMINATOR THE COMPILER WROTE. `kv: {distributed: …}`
	// compiles to a `type` of "kv", which is how a reader knows which of the
	// four operation surfaces this resource has without guessing from which
	// block is present.
	model := resource.String("type")
	if model == "" {
		return declared{}, fmt.Errorf("%w: %s declares no model", ErrNotADatabase, ref.id)
	}

	out := declared{
		revision: pointer.Revision,
		spec: database.Spec{
			Type:        database.Type(model),
			Engine:      resource.String(model, "engine"),
			Project:     ref.project,
			Environment: ref.environment,

			// THE ID AND NEVER THE LABEL, for the reason node/database.go
			// already gives: two workloads may call one database different
			// things, and two databases may both be called `tokens`.
			Name:     ref.storeName(),
			Address:  resource.String(model, "host"),
			Database: resource.String(model, "database"),
		},
		consensus: resource.String(model, "consensus"),
	}
	if n, ok := resource.Int(model, "size"); ok {
		out.size = n
	}

	// THE ENGINE THE SPEC NAMES IS THE ONE THAT KEEPS THE DECLARED RESOLUTION.
	// tau's own engine is `distributed` in the vocabulary either way; which
	// store backs it is this service's to decide and the registry's to be keyed
	// by. A connector names its own engine and keeps it.
	if named, served := servedConsensus[out.wants()]; served && out.spec.Engine == tauEngine {
		out.spec.Engine = named
	}

	// TLS IS ON UNLESS THE AUTHOR WROTE `disable`, which is the vocabulary's own
	// polarity, and `require` is served as verify-full because pkg/database
	// carries a bool and serving the weaker of the two would quietly accept a
	// network attacker as the server.
	out.spec.Credentials.TLS = resource.String(model, "tls") != "disable"

	// PER-MODEL DECLARATIONS TRAVEL AS OPTIONS. A vector's width and metric mean
	// nothing to the other three.
	for _, option := range []string{"dimensions", "metric"} {
		if value := resource.String(model, option); value != "" {
			out.option(option, value)
			continue
		}
		if n, ok := resource.Int(model, option); ok {
			out.option(option, fmt.Sprint(n))
		}
	}

	return out, nil
}

func (d *declared) option(name, value string) {
	if d.spec.Options == nil {
		d.spec.Options = map[string]string{}
	}
	d.spec.Options[name] = value
}
