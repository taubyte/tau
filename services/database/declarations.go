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
// Both are RequiredWhen the engine is distributed, and neither used to reach a
// runtime: services/worker carried engine, host, database, tls, dimensions and
// metric onto the grant, and nothing carried the other two. Reading the
// DECLARATION rather than the grant reads all of it, which is why they appear
// below and did not appear there.
//
// `consensus` is on the grant now as well, and for the other half of the same
// rule: a node that runs no database service opens a kv through its own
// engines, and one that could not see the word would keep a ledger as a crdt
// without saying so. Carrying it there buys a refusal by name, not a second
// opinion — what this service serves from is still read here.

// declared is one database as its author wrote it, plus the deployment that
// wrote it — the revision is what tells a warm engine it has been replaced.
type declared struct {
	spec     database.Spec
	revision uint64

	// size is the ceiling the author declared, in bytes. Zero means none was
	// written, which the vocabulary only allows for a connector.
	size int64
}

// served is what this service implements today.
//
// THE NAMES ARE THE SCHEMA'S. `database` declares a model — kv, sql, document,
// vector — and, for tau's own engine, how concurrent writes resolve. Those are
// the words an author writes and they are the words here; nothing is renamed on
// the way in and there is no second vocabulary to map between.
var (
	servedModels = []database.Type{database.KV}

	// servedConsensus is what a node keeps when its composer said nothing. A
	// crdt store is the one every node has — it is the replicated store every
	// other v2 store is built on — and raft needs a group to be opened in,
	// which a composition either hands this service or does not.
	servedConsensus = []string{database.CRDT}
)

// defaultConsensus is what a database that wrote none gets. `consensus` is
// RequiredWhen the engine is distributed, so this is for a connector, which
// resolves nothing because somebody else's server does.
const defaultConsensus = database.CRDT

// serves refuses a declaration this node cannot honour, naming which half of it
// is the problem.
//
// WHAT THIS NODE KEEPS AND NOT WHAT THE PACKAGE IMPLEMENTS. raft is served
// here, over a group per database — but only by a node that was composed with
// something to open a group in. Refusing at the door means an operator is told
// "this cloud does not keep that consensus" rather than discovering it as an
// unavailable database, which is the rule node/queue.go keeps for a crdt queue
// and node/cache.go for a cache.
//
// RAFT IS REFUSED AND NOT MERGED, wherever it is refused. An author writes it
// when entries must not resolve by merge — a ledger — and serving it as crdt
// would answer the one thing the field exists to prevent.
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
	if kind != specs.KindDatabase {
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

			// THE ID AND NEVER THE LABEL, for the reason node/database
			// already gives: two workloads may call one database different
			// things, and two databases may both be called `tokens`.
			Name:     ref.storeName(),
			Address:  block.host,
			Database: block.database,

			// RESOLVED HERE AND NOWHERE LATER. The store a resident engine is
			// handed is chosen by this word, so a hole in it would be a
			// database kept by whichever branch happened to be first.
			Consensus: consensusOf(block.consensus),
		},
	}
	if n := block.size; n != nil {
		out.size = *n
	}

	// TLS IS ON UNLESS THE AUTHOR WROTE `disable`, which is the vocabulary's own
	// polarity, and `require` is served as verify-full because pkg/database
	// carries a bool and serving the weaker of the two would quietly accept a
	// network attacker as the server.
	out.spec.Credentials.TLS = !block.plaintext

	// PER-MODEL DECLARATIONS TRAVEL AS OPTIONS. A vector's width and metric mean
	// nothing to the other three.
	if n := db.Vector.Dimensions; model == specs.DatabaseTypeVector && n != nil {
		out.option("dimensions", fmt.Sprint(*n))
	}
	if metric := db.Vector.Metric; model == specs.DatabaseTypeVector && metric != "" {
		out.option("metric", metric)
	}

	return out, nil
}

// typeBlock is the fields every data model's own block may carry, off
// whichever block the type names: the engines a type may have differ, which is
// the vocabulary's reason for putting them under the type, and a model that
// does not declare one reads it empty.
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
