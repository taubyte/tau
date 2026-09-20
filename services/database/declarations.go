package database

import (
	"context"
	"fmt"
	"slices"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
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

	// regions is where this database's copies go, already resolved: what the
	// resource's own region block declared, bounded by what its environment
	// allows. placement.go is asked about these and nothing else.
	regions []string
}

// served is what this service implements today.
//
// THE NAMES ARE THE SCHEMA'S. `database` declares a model — kv, sql, document,
// vector — and, for tau's own engine, how concurrent writes resolve. Those are
// the words an author writes and they are the words here; nothing is renamed on
// the way in and there is no second vocabulary to map between.
var (
	servedModels    = []database.Type{database.KV}
	servedConsensus = []string{"crdt"}
)

// defaultConsensus is what a database that wrote none gets. `consensus` is
// RequiredWhen the engine is distributed, so this is for a connector, which
// resolves nothing because somebody else's server does.
const defaultConsensus = "crdt"

// serves refuses a declaration this service cannot honour, naming which half of
// it is the problem.
//
// RAFT IS REFUSED AND NOT MERGED. An author writes it when entries must not
// resolve by merge — a ledger — and serving it as crdt would answer the one
// thing the field exists to prevent. What is missing is not the store but the
// thing that owns a raft group: cluster-service.md settled on 2026-08-31 that a
// group is a consistency domain owned by a service, scoped to a region, and not
// one group per service type. Until that exists, this cloud does not keep raft
// and says so.
func (d declared) serves() error {
	if !slices.Contains(servedModels, d.spec.Type) {
		return fmt.Errorf("%w: %s", ErrModelUnserved, d.spec.Type)
	}
	if !slices.Contains(servedConsensus, d.wants()) {
		return fmt.Errorf("%w: %q", ErrConsensusUnserved, d.wants())
	}
	return nil
}

// wants is the resolution this database asked for.
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

	// TLS IS ON UNLESS THE AUTHOR WROTE `disable`, which is the vocabulary's own
	// polarity, and `require` is served as verify-full because pkg/database
	// carries a bool and serving the weaker of the two would quietly accept a
	// network attacker as the server.
	out.spec.Credentials.TLS = resource.String(model, "tls") != "disable"

	// WHERE IT LIVES IS THE RESOURCE'S OWN STATEMENT, bounded by its
	// environment's. Reading it here and not on the grant is the same rule the
	// file doc gives for size and consensus: a caller that described the
	// database could describe a different one.
	region := placedOf(resource)
	if region.policy == "" {
		// THE PROJECT'S DEFAULT, which is where a project says it once for
		// everything that does not say it itself. Nothing lowered it into the
		// resource, so this is the walk up rather than a read of a copy — and
		// a resource with neither is one the audit already calls Broken.
		region = placedOf(project.Root())
	}
	if out.regions, err = region.regions(ref, allowanceOf(project.Environments(), ref.environment)); err != nil {
		return declared{}, fmt.Errorf("%s: %w", ref.id, err)
	}

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

// allowanceOf is the region ceiling a deployment declares: the list an
// environment keeps its state in, and the ceiling every resource under it is
// placed within. Empty means the cloud decides, which narrows nothing.
//
// A SHADOW HAS NO ALLOWANCE OF ITS OWN and takes the one it was written inside.
// It is a deployment over another environment's state, so placing that state
// was never its to do — which is why the vocabulary gives a shadow no `regions`
// field rather than a rule about ignoring one.
func allowanceOf(environments []schema.Environment, name string) []string {
	for _, env := range environments {
		if env.Name == name {
			return env.Regions
		}
		for _, shadow := range env.Shadows {
			if shadow.Name == name {
				return env.Regions
			}
		}
	}
	return nil
}

// placedOf reads a region block off whatever carries one — a resource, or the
// project it sits under. They are the same block, so this is one reader.
func placedOf(r specs.Resource) placed {
	return placed{
		policy:  r.String("region", "policy"),
		name:    r.String("region", "name"),
		main:    r.String("region", "main"),
		standby: r.String("region", "standby"),
	}
}
