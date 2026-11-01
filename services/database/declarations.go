package database

import (
	"context"
	"fmt"
	"slices"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// A call carries a deployment and a resource id; everything about the database
// (model, engine, location, size, consensus) is read here from the compiled
// project, so a caller cannot describe a different database.

// declared is one database as its author wrote it, plus the deployment that
// wrote it — the revision is what tells a warm engine it has been replaced.
type declared struct {
	spec     database.Spec
	revision uint64

	// size is the ceiling the author declared, in bytes. Zero means none was
	// written, which the vocabulary only allows for a connector.
	size int64
}

// served is what this service implements, in the schema's own words.
var (
	servedModels = []database.Type{database.KV}

	// servedConsensus is what a node keeps when its composer said nothing: crdt, which
	// every node has. raft needs a group to open in, which a composition may hand this
	// service.
	servedConsensus = []string{database.CRDT}
)

// defaultConsensus is what a database that wrote none gets. `consensus` is
// RequiredWhen the engine is distributed, so this is for a connector, which
// resolves nothing because somebody else's server does.
const defaultConsensus = database.CRDT

// serves refuses a declaration this node cannot honour, naming which half. It is
// what this node keeps, not what the package implements: raft needs a group opener.
// Raft is refused rather than merged as crdt.
func (d declared) serves(consensus []string) error {
	if !slices.Contains(servedModels, d.spec.Type) {
		return fmt.Errorf("%w: %s", ErrModelUnserved, d.spec.Type)
	}
	if !slices.Contains(consensus, d.wants()) {
		return fmt.Errorf("%w: %q", ErrConsensusUnserved, d.wants())
	}
	return nil
}

// wants is the resolution this database asked for, already defaulted — of()
// resolves it onto the spec, so the provider that picks a store is handed a
// spec that always says.
func (d declared) wants() string { return d.spec.Consensus }

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
		return declared{}, fmt.Errorf("database: reading %s/%s: %w", ref.project, ref.environment, err)
	}

	kind, scope, name, held := project.Locate(ref.id)
	if !held {
		return declared{}, fmt.Errorf("%w: %s", ErrNotDeclared, ref.id)
	}
	if kind != specs.KindDatabase {
		return declared{}, fmt.Errorf("%w: %s is a %s", ErrNotADatabase, ref.id, kind)
	}

	resource, _, err := project.Resource(scope, kind, name)
	if err != nil {
		return declared{}, fmt.Errorf("%w: %s: %w", ErrNotDeclared, ref.id, err)
	}

	// The model is the compiled `type`.
	db := specs.ReadDatabase(resource)
	model := db.Type
	if model == "" {
		return declared{}, fmt.Errorf("%w: %s declares no model", ErrNotADatabase, ref.id)
	}
	block := blockOf(db)

	out := declared{
		revision: pointer.Revision,
		spec: database.Spec{
			Type:        database.Type(model),
			Engine:      block.engine,
			Project:     ref.project,
			Environment: ref.environment,

			// The id, never the label: labels are per workload and not unique.
			Name:     ref.storeName(),
			Address:  block.host,
			Database: block.database,

			// Resolved here, since this word chooses the store a resident engine is handed.
			Consensus: consensusOf(block.consensus),
		},
	}
	if n := block.size; n != nil {
		out.size = *n
	}

	// TLS is on unless the author wrote `disable`. `require` is served as verify-full,
	// since pkg/database carries a bool and the weaker reading would accept a network
	// attacker.
	out.spec.Credentials.TLS = !block.plaintext

	// Per-model declarations travel as options.
	if n := db.Vector.Dimensions; model == specs.DatabaseTypeVector && n != nil {
		out.option("dimensions", fmt.Sprint(*n))
	}
	if metric := db.Vector.Metric; model == specs.DatabaseTypeVector && metric != "" {
		out.option("metric", metric)
	}

	return out, nil
}

// typeBlock is the fields every model's own block may carry, read off whichever
// block the type names.
type typeBlock struct {
	engine, host, database, consensus string
	size                              *int64

	// plaintext is the author writing `tls: disable`, the only way off.
	plaintext bool
}

func blockOf(db specs.Database) typeBlock {
	switch db.Type {
	case specs.DatabaseTypeSql:
		s := db.Sql
		return typeBlock{s.Engine, s.Host, s.Database, s.Consensus, s.Size, s.Tls == specs.DatabaseSqlTlsDisable}
	case specs.DatabaseTypeDocument:
		d := db.Document
		return typeBlock{d.Engine, d.Host, d.Database, d.Consensus, d.Size, d.Tls == specs.DatabaseDocumentTlsDisable}
	case specs.DatabaseTypeKv:
		return typeBlock{engine: db.Kv.Engine, consensus: db.Kv.Consensus, size: db.Kv.Size}
	case specs.DatabaseTypeVector:
		return typeBlock{engine: db.Vector.Engine, consensus: db.Vector.Consensus, size: db.Vector.Size}
	}
	return typeBlock{}
}

// consensusOf is what the author wrote, or what a database that wrote nothing
// gets.
func consensusOf(written string) string {
	if written == "" {
		return defaultConsensus
	}
	return written
}

func (d *declared) option(name, value string) {
	if d.spec.Options == nil {
		d.spec.Options = map[string]string{}
	}
	d.spec.Options[name] = value
}
