package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/node/ledger"
	"github.com/taubyte/tau/pkg/database"
	docengine "github.com/taubyte/tau/pkg/database/engines/document"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	vecengine "github.com/taubyte/tau/pkg/database/engines/vector"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/specs"
	databaseService "github.com/taubyte/tau/services/database"
)

// database.go backs `taubyte:sql/store` on this node, over pkg/database.
//
// # No driver is linked here, and that is pkg/database's decision rather than
// # this file's
//
// database/sql is a registry: a driver registers itself from its own init, so a
// deployment that wants Postgres imports one and registers the engine —
//
//	import _ "github.com/jackc/pgx/v5/stdlib"
//	node.WithDatabaseEngines(reg)  // reg holds sql.Postgres("pgx")
//
// This repository therefore links no database driver, which is what lets the
// same binary serve a cloud that has one and a cloud that does not. A node with
// nothing registered answers every database call by naming what IS registered,
// which is a message an operator can act on rather than a connection that hangs.
//
// # The credential is resolved here and nowhere earlier
//
// The compiled resource carries a REFERENCE to a secret. It is resolved at the
// moment a connection is opened, through the same vault the worker reads a
// run-time variable from — so a password is never in a spec, which is logged,
// cached, compared and handed to a tier.

// TauBacked is every data model this node opens against the swarm's own store,
// rather than against a server somebody else runs.
//
// EXPORTED SO THE VOCABULARY CAN BE HELD TO IT. schema.DatabaseEngines() offers
// tau's own engine for EVERY model, `sql` included — and nothing here opens a
// distributed sql, so `sql: {engine: distributed}` compiled, deployed, and
// answered every query with "no database engine is registered in this build".
// The one test that checks engine coverage skipped tau's own engine for every
// model, which is exactly where that hid.
func TauBacked() []database.Type {
	return []database.Type{database.KV, database.Document, database.Vector}
}

func tauBacks(want database.Type) bool {
	for _, model := range TauBacked() {
		if model == want {
			return true
		}
	}
	return false
}

// databases is the node's Databases and KVs: one registry, one connection per
// handle, whichever data model the grant names.
type databases struct {
	node *Node

	mu       sync.Mutex
	open     map[string]database.Conn
	registry *database.Registry

	// storesMu guards stores, and it is NOT d.mu. openFor holds d.mu across
	// registry.Open, which reaches storesFor — one mutex for both would
	// deadlock on the first database anybody opened.
	storesMu sync.Mutex
	stores   map[string]kvdb.KVDB

	// ledgers is the other half of the consensus axis: the groups a
	// `consensus: raft` database's entries are ordered in. It is nil on a node
	// composed with no way to open one, which is what ErrNoRaft names.
	ledgers *ledger.Ledgers
}

// newDatabases wires one backing over what this node was given.
//
// THE GROUP TABLE IS BUILT HERE AND NOT ON FIRST USE, because building it costs
// nothing — it opens no group, which is the promise node/ledger keeps — and
// because a table built lazily would have to be built again after Close, on a
// node whose groups are gone.
func newDatabases(n *Node) (*databases, error) {
	d := &databases{node: n}
	if n.opts.raft == nil {
		return d, nil
	}

	ledgers, err := ledger.New(n.opts.raft, ledger.Budget)
	if err != nil {
		return nil, fmt.Errorf("node: %s: %w", Database, err)
	}
	d.ledgers = ledgers
	return d, nil
}

var (
	_ wasmtier.SQL            = (*databases)(nil)
	_ wasmtier.KVs            = (*databases)(nil)
	_ wasmtier.DocumentStores = (*databases)(nil)
	_ wasmtier.VectorStores   = (*databases)(nil)
)

var (
	// ErrNoEngines is a node asked for a database with no engine registered.
	ErrNoEngines = errors.New("node: no database engine is registered in this build")

	// ErrNotSQL is a database whose data model this tier does not serve yet.
	ErrNotSQL = errors.New("node: only the sql data model is served")

	// ErrConsensusUnserved is a ledger reached through this node's OWN engines
	// rather than through the database service.
	//
	// IT IS REFUSED BY NAME AND NEVER MERGED. These engines are the fallback
	// for a node that runs no database service and can reach none, and a raft
	// database has nothing to keep its order in there: no group, no owner, and
	// nowhere to send a write that this node may not make. Serving it as crdt
	// would resolve two entries into one with no error to anybody, which is the
	// one thing the declaration exists to prevent.
	ErrConsensusUnserved = errors.New("node: a raft database is kept by the database service and not by this node's own engines")
)

// Query runs a statement that reads.
func (d *databases) Query(ctx context.Context, grant invoke.Grant, statement string, args []any) (wasmtier.Rows, error) {
	conn, err := d.conn(ctx, grant)
	if err != nil {
		return wasmtier.Rows{}, err
	}

	rows, err := conn.Query(ctx, statement, args...)
	if err != nil {
		return wasmtier.Rows{}, refused(err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return wasmtier.Rows{}, refused(err)
	}

	// ROW-MAJOR AND FLAT, which is the shape the ABI carries — see the
	// interface doc for why it is not a list of lists.
	out := wasmtier.Rows{Columns: columns}
	for rows.Next() {
		cells := make([]any, len(columns))
		into := make([]any, len(columns))
		for i := range cells {
			into[i] = &cells[i]
		}
		if err := rows.Scan(into...); err != nil {
			return wasmtier.Rows{}, refused(err)
		}
		out.Values = append(out.Values, cells...)
	}
	if err := rows.Err(); err != nil {
		return wasmtier.Rows{}, refused(err)
	}
	return out, nil
}

// Exec runs a statement that writes and answers with what it changed.
func (d *databases) Exec(ctx context.Context, grant invoke.Grant, statement string, args []any) (wasmtier.Outcome, error) {
	conn, err := d.conn(ctx, grant)
	if err != nil {
		return wasmtier.Outcome{}, err
	}

	outcome, err := conn.Exec(ctx, statement, args...)
	if err != nil {
		return wasmtier.Outcome{}, refused(err)
	}
	return wasmtier.Outcome(outcome), nil
}

// refused marks an error as the SERVER'S answer rather than a failure to reach
// it, so the tier can tell a caller to fix a statement instead of retrying one.
//
// EVERYTHING FROM AN OPEN CONNECTION IS THE SERVER'S. Reaching it already
// succeeded; a driver that lost the connection mid-statement is rare enough
// that reporting it as the server's "no" costs a retry nobody was going to make
// anyway, and the alternative — parsing driver errors to classify them — is a
// table that drifts per driver.
func refused(err error) error {
	return fmt.Errorf("%w: %s", wasmtier.ErrStatementFailed, err.Error())
}

// conn opens this database, once, and keeps it.
//
// A CONNECTION IS KEPT BECAUSE A POOL IS WHAT database/sql ALREADY IS: the
// handle behind it pools, so opening per invocation would discard the pool on
// every request and hand the server a new connection each time.
func (d *databases) conn(ctx context.Context, grant invoke.Grant) (database.SQLConn, error) {
	opened, err := d.openFor(ctx, grant, database.SQL)
	if err != nil {
		return nil, err
	}

	sqlConn, ok := opened.(database.SQLConn)
	if !ok {
		return nil, fmt.Errorf("%w: %s answers no sql surface", ErrNotSQL, grant.Settings["sql/engine"])
	}
	return sqlConn, nil
}

// OpenKV is the kv half of this backing: the same registry, the same one
// connection per handle, a different data model.
func (d *databases) OpenKV(ctx context.Context, grant invoke.Grant) (wasmtier.KV, error) {
	opened, err := d.openFor(ctx, grant, database.KV)
	if err != nil {
		return nil, err
	}

	conn, ok := opened.(database.KVConn)
	if !ok {
		return nil, fmt.Errorf("node: %s answers no kv surface", grant.Settings["kv/engine"])
	}
	return keyValue{conn}, nil
}

// OpenDocuments is the document half of this backing.
func (d *databases) OpenDocuments(ctx context.Context, grant invoke.Grant) (wasmtier.Documents, error) {
	opened, err := d.openFor(ctx, grant, database.Document)
	if err != nil {
		return nil, err
	}

	conn, ok := opened.(database.DocumentConn)
	if !ok {
		return nil, fmt.Errorf("node: %s answers no document surface", grant.Settings["document/engine"])
	}
	return documents{conn}, nil
}

// documents adapts pkg/database's DocumentConn to the tier's, which speaks
// BYTES: the ABI carries a document as JSON because the canonical ABI has no
// arbitrary map, so this is where the two meet.
type documents struct{ database.DocumentConn }

func (d documents) Get(ctx context.Context, id string) ([]byte, error) {
	doc, err := d.DocumentConn.Get(ctx, id)
	if err != nil {
		return nil, documentError(err)
	}
	return json.Marshal(doc)
}

func (d documents) Put(ctx context.Context, id string, raw []byte) error {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: %s", wasmtier.ErrMalformed, err)
	}
	return documentError(d.DocumentConn.Put(ctx, id, doc))
}

func (d documents) Delete(ctx context.Context, id string) error {
	return documentError(d.DocumentConn.Delete(ctx, id))
}

// Find takes a JSON filter and answers with a JSON array.
//
// AN EMPTY FILTER IS EVERYTHING, and an empty BODY is an empty filter — a guest
// that wants the whole collection should not have to send `{}`.
func (d documents) Find(ctx context.Context, raw []byte) ([]byte, error) {
	var filter map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &filter); err != nil {
			return nil, fmt.Errorf("%w: %s", wasmtier.ErrMalformed, err)
		}
	}

	found, err := d.DocumentConn.Query(ctx, filter)
	if err != nil {
		return nil, documentError(err)
	}
	if found == nil {
		// AN EMPTY ARRAY AND NOT `null`, so a guest can parse the answer the
		// same way whether or not anything matched.
		found = []map[string]any{}
	}
	return json.Marshal(found)
}

// documentError translates the engine's sentinels into the tier's, which is the
// same job node/cache.go does and the same bug it was written to stop.
func documentError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, docengine.ErrNotFound):
		return fmt.Errorf("%w: %w", wasmtier.ErrNotFound, err)
	case errors.Is(err, docengine.ErrNotAnObject):
		return fmt.Errorf("%w: %w", wasmtier.ErrMalformed, err)
	default:
		return err
	}
}

// OpenVectors is the vector half of this backing.
func (d *databases) OpenVectors(ctx context.Context, grant invoke.Grant) (wasmtier.Vectors, error) {
	opened, err := d.openFor(ctx, grant, database.Vector)
	if err != nil {
		return nil, err
	}

	conn, ok := opened.(database.VectorConn)
	if !ok {
		return nil, fmt.Errorf("node: %s answers no vector surface", grant.Settings["vector/engine"])
	}
	return vectors{conn}, nil
}

// vectors adapts pkg/database's VectorConn to the tier's. The embedding is
// already a float slice on both sides; what differs is the META, which the ABI
// carries as JSON for document.go's reason.
type vectors struct{ database.VectorConn }

func (v vectors) Search(ctx context.Context, query []float32, k int) ([]wasmtier.Match, error) {
	found, err := v.VectorConn.Search(ctx, query, k)
	if err != nil {
		return nil, vectorError(err)
	}

	out := make([]wasmtier.Match, 0, len(found))
	for _, m := range found {
		match := wasmtier.Match{ID: m.ID, Score: m.Score}
		if len(m.Meta) > 0 {
			raw, err := json.Marshal(m.Meta)
			if err != nil {
				return nil, fmt.Errorf("%w: %s", wasmtier.ErrMalformed, err)
			}
			match.Meta = raw
		}
		out = append(out, match)
	}
	return out, nil
}

// Upsert takes the meta as JSON. AN EMPTY META IS NO META rather than an empty
// object, so a guest that has nothing to attach sends nothing.
func (v vectors) Upsert(ctx context.Context, id string, embedding []float32, meta []byte) error {
	var attached map[string]any
	if len(meta) > 0 {
		if err := json.Unmarshal(meta, &attached); err != nil {
			return fmt.Errorf("%w: %s", wasmtier.ErrMalformed, err)
		}
	}
	return vectorError(v.VectorConn.Upsert(ctx, id, embedding, attached))
}

func (v vectors) Delete(ctx context.Context, id string) error {
	return vectorError(v.VectorConn.Delete(ctx, id))
}

// vectorError translates the engine's sentinels into the tier's.
func vectorError(err error) error {
	if err != nil && errors.Is(err, vecengine.ErrWidth) {
		return fmt.Errorf("%w: %w", wasmtier.ErrWrongWidth, err)
	}
	return err
}

// keyValue adapts pkg/database's KVConn to what the tier asks for. The two
// differ in one name — `List` there, `Keys` here — because `list` is a WIT
// keyword and the guest's function could not be called that.
type keyValue struct{ database.KVConn }

func (k keyValue) Keys(ctx context.Context, prefix string) ([]string, error) {
	return k.KVConn.List(ctx, prefix)
}

func (k keyValue) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := k.KVConn.Get(ctx, key)
	if err != nil && errors.Is(err, kvengine.ErrNotFound) {
		// THE TIER'S MISS AND NOT THE ENGINE'S, which is the same translation
		// node/cache.go makes and the same bug it was written to stop: an
		// unrecognised error becomes `unavailable`, so every miss would read
		// as an outage.
		return nil, fmt.Errorf("%w: %w", wasmtier.ErrNotFound, err)
	}
	return value, err
}

// openFor resolves one grant into a connection of the given data model, and
// keeps it.
//
// A CONNECTION IS KEPT BECAUSE A POOL IS WHAT database/sql ALREADY IS, and
// because a kv connection is a prefix over a store that is already open —
// re-deriving either per invocation would be work for no answer.
func (d *databases) openFor(ctx context.Context, grant invoke.Grant, want database.Type) (database.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if held, ok := d.open[grant.Handle]; ok {
		return held, nil
	}
	registry, err := d.engines(ctx, want)
	if err != nil {
		return nil, err
	}

	spec, err := d.specOf(ctx, grant, want)
	if err != nil {
		return nil, err
	}

	opened, err := registry.Open(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("node: opening %s %q: %w", spec.Engine, grant.Label, err)
	}

	if d.open == nil {
		d.open = map[string]database.Conn{}
	}
	d.open[grant.Handle] = opened
	return opened, nil
}

// specOf turns a grant into what pkg/database opens from.
func (d *databases) specOf(ctx context.Context, grant invoke.Grant, want database.Type) (database.Spec, error) {
	model := grant.Settings[databaseTypeSetting]
	if database.Type(model) != want {
		return database.Spec{}, fmt.Errorf("%w: %q is %q", ErrNotSQL, grant.Label, model)
	}

	project, environment, id := specs.SplitHandle(grant.Handle)
	spec := database.Spec{
		Type:   want,
		Engine: grant.Settings[model+"/engine"],

		Project:     project,
		Environment: environment,

		// THE RESOLVED ID AND NOT THE AUTHOR'S LABEL. A label is local to the
		// resource that wrote it — two workloads may call one database
		// different things, and two different databases may both be called
		// `tokens` — so a kv engine keying its rows on the label would put two
		// databases in one key space. It is the id for every engine and not
		// just that one, because Spec.Name is what ADDRESSES the resource.
		Name:     databaseName(id),
		Address:  grant.Settings[model+"/host"],
		Database: grant.Settings[model+"/database"],

		// WHAT THE AUTHOR DECLARED ABOUT CONCURRENT WRITES, carried by
		// services/worker onto the grant. Reading it is what lets the refusal
		// below be by name.
		Consensus: grant.Settings[model+"/consensus"],
	}

	if spec.Consensus == database.Raft {
		return database.Spec{}, fmt.Errorf("%w: %s", ErrConsensusUnserved, grant.Label)
	}

	// TLS IS ON UNLESS THE AUTHOR WROTE `disable`, which is the vocabulary's
	// own polarity: absent means verify-full and turning it off is a word
	// somebody had to write.
	//
	// `require` IS SERVED AS verify-full, which is STRONGER than asked.
	// pkg/database carries a bool and cannot say "encrypted but unverified";
	// serving the weaker of the two would be quietly accepting a network
	// attacker as the server, so the stronger one is what happens — and a
	// server with a certificate this node will not verify fails to connect,
	// loudly, instead of connecting to whoever answered.
	spec.Credentials.TLS = grant.Settings[model+"/tls"] != "disable"

	// PER-TYPE DECLARATIONS TRAVEL AS OPTIONS. A vector's width and metric mean
	// nothing to the other three models, which is why they are a map here and
	// not fields on the Spec.
	for _, option := range []string{"dimensions", "metric"} {
		if value := grant.Settings[model+"/"+option]; value != "" {
			if spec.Options == nil {
				spec.Options = map[string]string{}
			}
			spec.Options[option] = value
		}
	}

	if reference := grant.Settings[model+"/"+databaseCredentialsSetting]; reference != "" {
		username, password, err := d.credentials(ctx, project, environment, reference)
		if err != nil {
			return database.Spec{}, err
		}
		spec.Credentials.Username, spec.Credentials.Password = username, password
	}
	return spec, nil
}

// databaseName is the id an engine keys its rows under.
//
// AN ADMITTED INSTANCE IS PART OF THE HANDLE — `<project>/<env>/<id>/<instance>`
// — which every backing that prefixes by handle isolates for free. A database
// is the one that does not: pkg/database's engines build a key prefix from
// Spec.Name and REFUSE a name containing "/", so an instance's handle made
// every kv, document and vector database permanently unopenable, with the
// error cached under the handle for the life of the node.
//
// So the separator is folded here, where the handle is read back apart, rather
// than changed in the handle — which node/cache, node/storage and the gateway's
// bus attach all depend on. A resource id is a CID and carries no
// ":", and an instance name is already refused if it carries a "/", so the
// mapping is injective and two instances never share a key space.
func databaseName(id string) string { return strings.ReplaceAll(id, "/", ":") }

// engines is the registry this node serves from, with tau's own kv engine in
// it.
//
// THE kv ENGINE IS REGISTERED BY THIS NODE AND NOT BY A COMPOSER, which is what
// makes `type: kv` work with nothing to provision: a project declares a
// database and gets one from the swarm it is already on. Every other engine
// reaches a server somebody else runs, so those are the composer's to register
// — see WithDatabaseEngines.
//
// It is opened on FIRST USE. A node that serves no kv database never opens the
// store, which is the same rule the cache backing keeps.
func (d *databases) engines(ctx context.Context, want database.Type) (*database.Registry, error) {
	if d.registry == nil {
		d.registry = d.node.opts.engines
	}
	// TAU'S OWN ENGINES ARE THIS NODE'S TO REGISTER, and the rest are a
	// composer's — see the doc above. kv, document and vector are backed by the
	// swarm's own store; sql is not, and TauBacked is where that is said once
	// so a test can hold the vocabulary to it.
	if !tauBacks(want) {
		if d.registry == nil {
			return nil, ErrNoEngines
		}
		return d.registry, nil
	}

	if d.registry == nil {
		d.registry = database.NewRegistry()
	}
	if len(d.registry.EnginesFor(want)) > 0 {
		// Already there — either this call registered it, or a composer
		// registered their own, and theirs wins.
		return d.registry, nil
	}

	if d.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}

	var engine database.Engine
	var err error
	switch want {
	case database.KV:
		engine, err = kvengine.New(d.storesFor)
	case database.Document:
		engine, err = docengine.New(d.storesFor)
	default:
		engine, err = vecengine.New(d.storesFor)
	}
	if err != nil {
		return nil, err
	}
	if err := d.registry.Register(ctx, engine); err != nil {
		return nil, fmt.Errorf("node: registering tau's own %s engine: %w", want, err)
	}
	return d.registry, nil
}

// databaseStore is the prefix every database's own kvdb is named under.
//
// SEPARATE FROM THE CACHES', because they are different kinds with different
// lifetimes: a cache evicts and a database does not, and one CRDT holding both
// would make a cache's churn the database's replication traffic.
const databaseStore = "databases"

// The setting keys services/worker writes.
const (
	databaseTypeSetting = "type"
	// UNDER THE MODEL, as `sql/credentials/name` — which is where the vocabulary
	// declares it and where every other connection field already sat. A bare
	// `credentials/name` is a key `database` does not have.
	databaseCredentialsSetting = "credentials/name"
)

// credentials reads the secret the configuration referenced.
//
// THE SECRET IS "username:password", split on the FIRST colon — the userinfo
// shape a connection URL already uses, and the one that lets a password contain
// a colon. A secret with no colon is a password with no user, which is what a
// server that authenticates by password alone wants.
//
// The vocabulary says only "Secret holding the credentials to connect with", so
// this is a convention rather than a rule it states. It is written down here
// because the alternative — inventing a username — is worse.
func (d *databases) credentials(ctx context.Context, project, environment, reference string) (username, password string, err error) {
	vault := newVault(d.node)
	defer vault.Close()

	value, err := vault.Secret(ctx, project, environment, reference)
	if err != nil {
		return "", "", fmt.Errorf("node: reading the credentials for %s/%s: %w", project, environment, err)
	}

	if user, pass, found := strings.Cut(value, ":"); found {
		return user, pass, nil
	}
	return "", value, nil
}

// Close releases every connection this node opened.
func (d *databases) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var err error
	for _, conn := range d.open {
		err = errors.Join(err, conn.Close())
	}
	d.open = nil

	// THE GROUPS THIS BACKING OPENED GO WITH IT. A raft cluster left running
	// keeps a protocol handler, a heartbeat and a log open on a peer nobody is
	// using any more.
	if d.ledgers != nil {
		err = errors.Join(err, d.ledgers.Close())
	}
	return err
}

// openDatabase starts this node's database service, when it runs one.
//
// THE ENGINES ARE THIS NODE'S AND THE DECLARATIONS ARE THE REGISTER'S, which is
// the split the service was built for: it opens what a project declared, and it
// reads what a project declared rather than being told.
func (n *Node) openDatabase(ctx context.Context) error {
	if !n.cfg.runs(Database) {
		return nil
	}

	// BOTH HALVES OF THE CONSENSUS AXIS, from the two things this node already
	// has: the replicated CRDT store every other v2 store is built on, and the
	// raft cluster the queues and crons run in. A node missing either serves
	// the other and refuses what it cannot keep, which is the point — a ledger
	// that asked for raft must not be merged.
	if n.opts.databases == nil {
		return fmt.Errorf("node: %s: %w", Database, ErrNoCacheStore)
	}

	// THE GROUP IS OPENED ON FIRST USE AND NOT HERE. A node builds its services
	// before it starts them, and raft finds its peers over a swarm that is not
	// up yet — a group opened at construction sits with no known peers, aborts
	// its election, and answers every write with "no leader". node/queue.go
	// opens its own in the background for the same reason.
	//
	// ONE STORE TABLE FOR THE NODE, shared by the engine that serves this
	// node's databases and the holdings that let go of them. Two tables is how
	// a destroyed store stays warm and serving: the eviction lands in one of
	// them and every later open comes out of the other.
	stores, err := newDatabases(n)
	if err != nil {
		return err
	}
	engine, err := kvengine.New(stores.storesFor)
	if err != nil {
		return fmt.Errorf("node: %s: %w", Database, err)
	}
	registry := database.NewRegistry()
	if err := registry.Register(ctx, engine); err != nil {
		return fmt.Errorf("node: %s: %w", Database, err)
	}

	// WHAT THIS NODE CAN ACTUALLY KEEP, which is a narrower question than what
	// the service implements. Both resolutions are served there; raft needs a
	// group to be opened in, and a node composed without one must refuse a
	// ledger BY NAME rather than answer "unavailable" about it forever.
	kept := []string{database.CRDT}
	if n.opts.raft != nil {
		kept = append(kept, database.Raft)
	}

	svc, err := databaseService.New(n.opts.host,
		databaseService.WithEngines(registry),
		databaseService.WithConsensus(kept...),
		databaseService.WithRegister(n.registerSource()),
		// WHAT THIS NODE ACTUALLY HOLDS, which is a different question from what
		// it can open: a node answers "do you hold this" from its disk, and is
		// the only thing that can let go of a copy.
		databaseService.WithHoldings(nodeHoldings{d: stores}),
		databaseService.WithRegion(n.cfg.Region),

		// THE FLEET AS THIS NODE SEES IT, asked each sweep rather than taken
		// once: a snapshot at construction would place every database on the
		// fleet as it was when the process started. A node running no stats
		// sees no fleet and moves nothing, which is the honest answer for a
		// node that cannot tell who else is there.
		databaseService.WithMembers(func() map[string][]placement.Member {
			if n.stats == nil {
				return nil
			}
			return n.stats.Placement()
		}),
	)
	if err != nil {
		return err
	}

	n.database = svc
	// THE GROUPS THIS NODE OPENED GO WITH THE SERVICE THAT OPENED THEM. A raft
	// cluster left running after the node stopped keeps a protocol handler, a
	// heartbeat and a log open on a peer nobody is using any more.
	n.closers = append(n.closers, stores)
	n.closers = append(n.closers, svc)
	return nil
}

// kvBacking is what the wasm tier opens a kv through.
//
// THE SERVICE WHEN THERE IS ONE, and this node's own engines otherwise. The
// second half is what v1 did everywhere and is kept only for a node that runs
// no database service and can reach none — a single-process test, a composition
// that predates the service. A node that runs the service takes Local(), which
// is the absence of a hop rather than a shortcut.
//
// REACHING ONE IN THE FLEET IS NOT HERE YET. A node with no database service of
// its own still falls back, so the data plane is still the worker's on that
// node — that is the remaining half of the cutover, and it is stated rather
// than papered over.
func (n *Node) kvBacking(own wasmtier.KVs) wasmtier.KVs {
	if n.database != nil {
		return n.database.Local()
	}
	return own
}

// storesFor is the provider every resident engine on this node is handed: the
// kvdb ONE database's entries live in.
//
// A KVDB PER DATABASE IS A PUBSUB TOPIC PER DATABASE, which is the point rather
// than a side effect — see database.Stores. One store for all of them meant
// every subscriber held every database's rows, so placement decided nothing,
// and a Delete was a tombstone every replica applied, so dropping a copy this
// node no longer holds deleted the copy the node that just acquired it holds.
//
// The factory caches by path, so two opens of one database share a store and a
// node carries as many as it HOLDS rather than as many as exist.
func (d *databases) storesFor(_ context.Context, spec database.Spec) (database.Store, error) {
	// WHICH KIND OF STORE IS THE DATABASE'S OWN DECLARATION AND NOT THIS NODE'S
	// CHOICE. `consensus` is the axis the vocabulary declares beside the model,
	// and it reaches here on the spec rather than as a second engine, because
	// the schema declares ONE engine for tau's own.
	//
	// A LEDGER IS A STORE TOO, which is the whole of the difference between the
	// two resolutions — see node/ledger, where the group, the leadership and the
	// barrier are. What is decided here is only which of the two things this
	// node holds keeps a database's entries, and ErrNoRaft is the answer for a
	// node composed with neither.
	if spec.Consensus == database.Raft {
		if d.ledgers == nil {
			return nil, ErrNoRaft
		}
		return d.ledgers.Store(spec)
	}

	// NAMED BY THE SAME FUNCTION THAT ADDRESSES IT AND THAT RELEASES IT.
	// Spec.Prefix already names a database uniquely and already refuses a
	// separator in any segment, which is exactly what a topic name needs — and
	// storeName is what nodeHoldings.Destroy evicts by, so the two agree by
	// construction rather than by two expressions that happen to match.
	name, err := storeName(spec)
	if err != nil {
		return nil, err
	}

	d.storesMu.Lock()
	defer d.storesMu.Unlock()

	if d.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}
	if db, held := d.stores[name]; held {
		return cacheStore{db}, nil
	}

	db, err := d.node.opts.databases.New(logger, name, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the store for %s: %w", name, err)
	}
	if d.stores == nil {
		d.stores = map[string]kvdb.KVDB{}
	}
	d.stores[name] = db
	d.node.addCloser(closerFunc(db.Close))
	return cacheStore{db}, nil
}

// nodeHoldings answers services/database's Holdings over this node's own store.
//
// IT ASKS THE DISK AND NOT THE TABLE of stores opened this run. A restarted node
// holds everything it held before and has opened none of it — a holder that
// answered from its warm table would say it holds nothing, and every move that
// needed it would either wait forever or, worse, count its "not held" as a
// reason for somebody else to let go.
type nodeHoldings struct{ d *databases }

// probe is the factory's disk-level surface. It is asserted rather than
// required of kvdb.Factory: holding is a fact about a NODE's bytes, and the
// shared interface is also implemented by handles that keep none.
type probe interface {
	Holding(ctx context.Context, path string) (bool, error)
	Paths(ctx context.Context, under string, depth int) ([]string, error)
}

func (h nodeHoldings) probe() (probe, bool) {
	p, ok := h.d.node.opts.databases.(probe)
	return p, ok
}

// storeName is the one name a database is known by here — the same function
// that addresses it and names its topic.
func storeName(spec database.Spec) (string, error) {
	prefix, err := spec.Prefix()
	if err != nil {
		return "", err
	}
	return databaseStore + "/" + strings.TrimSuffix(prefix, "/"), nil
}

func (h nodeHoldings) Held(ctx context.Context, spec database.Spec) bool {
	name, err := storeName(spec)
	if err != nil {
		return false
	}

	p, ok := h.probe()
	if !ok {
		// A COMPOSER'S FACTORY THAT CANNOT BE ASKED HOLDS NOTHING, as far as
		// anybody else is concerned. Answering "yes" on a guess would let a
		// peer drop the last copy.
		return false
	}
	held, err := p.Holding(ctx, name)
	return err == nil && held
}

func (h nodeHoldings) Destroy(ctx context.Context, spec database.Spec) error {
	name, err := storeName(spec)
	if err != nil {
		return err
	}

	h.d.storesMu.Lock()
	db, open := h.d.stores[name]
	delete(h.d.stores, name)
	h.d.storesMu.Unlock()

	if !open {
		// NOT OPEN IS NOT ABSENT — a restarted node holds bytes it has not
		// opened — so it is opened for exactly as long as it takes to destroy.
		if db, err = h.d.node.opts.databases.New(logger, name, rebroadcastSeconds); err != nil {
			return err
		}
	}

	destroyer, ok := db.(interface {
		Destroy(context.Context) error
	})
	if !ok {
		return fmt.Errorf("node: this store cannot release a database it holds")
	}
	return destroyer.Destroy(ctx)
}

func (h nodeHoldings) OnDisk(ctx context.Context) []database.Spec {
	p, ok := h.probe()
	if !ok {
		return nil
	}

	// FOUR SEGMENTS BELOW THE PREFIX, which is what Spec.Prefix writes:
	// project, environment, model, name. Everything under that is the CRDT's
	// own structure and is none of this function's business.
	paths, err := p.Paths(ctx, databaseStore, 4)
	if err != nil {
		return nil
	}

	out := make([]database.Spec, 0, len(paths))
	for _, path := range paths {
		parts := strings.Split(strings.TrimPrefix(path, databaseStore+"/"), "/")
		if len(parts) != 4 {
			continue
		}
		out = append(out, database.Spec{
			Project: parts[0], Environment: parts[1],
			Type: database.Type(parts[2]), Name: parts[3],
		})
	}
	return out
}
