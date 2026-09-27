package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/database"
	docengine "github.com/taubyte/tau/pkg/database/engines/document"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	vecengine "github.com/taubyte/tau/pkg/database/engines/vector"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/raft"
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

	// groupsMu guards groups, and it is its own for storesMu's reason: forming
	// a group is a background job that takes this lock to record what it made,
	// and a lock shared with the store table would hold every other database's
	// open behind one election.
	groupsMu sync.Mutex
	groups   map[string]*group

	// groupsGone is a node that has closed its groups. IT IS NOT "THE MAP IS
	// EMPTY": closeGroups empties it, and a request already past the resident
	// lookup would otherwise open a fresh group afterwards -- a live voter,
	// with a protocol handler and a heartbeat and a vote in a quorum, that
	// nothing will ever close again.
	groupsGone bool
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
	return errors.Join(err, d.closeGroups())
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
	stores := &databases{node: n}
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
	if spec.Consensus == database.Raft {
		return d.ledgerFor(spec)
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

// ─────────────────────────── `consensus: raft` ───────────────────────────
//
// A ledger is a database.Store over a raft group, which is the whole of the
// difference between the two resolutions: pkg/database's kv engine is generic
// over four methods and does the per-database prefixing itself, so every other
// property of a kv database is unchanged by construction rather than by a
// second implementation agreeing with the first.
//
// ONE GROUP PER DATABASE, and the cost of that is real: each namespace is its
// own libp2p protocol, its own heartbeat set, its own log and its own snapshot
// store. What is NOT affordable is the alternative. One group for every ledger
// on a node would serialize the cloud's writes through one leader, put every
// project's entries in every voter's state machine — making placement a
// decoration, which is the failure database.Stores was rewritten to end — and
// make leaving it RemoveServer over everybody's rows at once, with no local
// drop to reach for. What bounds the count is placement: a node opens a group
// only for the ledgers it is a voter for, which is the same bound a topic per
// database already has.

// raftDomain is where a ledger's group lives. It is NOT the queues' namespace:
// a project's entries and this cloud's own work are different lifetimes,
// different sizes and different blast radii — node/queue.go's argument, and it
// holds harder here because a ledger is somebody's system of record.
const raftDomain = "/tau/databases/v1"

// raftOperation is what one write or one barrier may cost.
//
// IT IS A REQUEST'S BUDGET AND NOT AN ELECTION'S. A group that has no leader is
// refused immediately and the caller retries; this bounds the round trip to a
// leader that exists, which is a commit to a quorum.
const raftOperation = 10 * time.Second

// groupName is the raft namespace one database's group lives in — the same name
// storeName gives its crdt store, under a different root, so the group and the
// store agree by construction rather than by two expressions that happen to
// match. Spec.Prefix already refuses a separator in any segment, which is what
// a protocol id needs.
func groupName(spec database.Spec) (string, error) {
	prefix, err := spec.Prefix()
	if err != nil {
		return "", err
	}
	return raftDomain + "/" + strings.TrimSuffix(prefix, "/"), nil
}

// group is one database's cluster and how far along opening it is.
type group struct {
	// forming is closed once the cluster has been opened or has failed to open,
	// so a caller can tell "not yet" from "not ever" without waiting.
	forming chan struct{}

	cluster raft.Cluster
	err     error
}

// ledgerFor is the store a `consensus: raft` database keeps its entries in.
func (d *databases) ledgerFor(spec database.Spec) (database.Store, error) {
	namespace, err := groupName(spec)
	if err != nil {
		return nil, err
	}
	if d.node.opts.raft == nil {
		return nil, ErrNoRaft
	}

	// FORMING STARTS AT THE OPEN AND NOT AT THE FIRST OPERATION, because it is
	// the longest thing that happens to a ledger and the sooner it starts the
	// sooner the first write lands. It does not block: see groupFor.
	d.groupFor(namespace)

	return &raftStore{
		group:   func() (raft.Cluster, error) { return d.groupFor(namespace) },
		timeout: raftOperation,
	}, nil
}

// groupFor answers a database's group, starting its formation if nothing has.
//
// IT NEVER BLOCKS. raft.New waits up to thirty seconds for peers to appear so
// that a cloud does not bootstrap a single-node cluster it later has to heal —
// right at boot and wrong on a request path, where node/queue.go measured what
// it costs: the first caller paid it, every other caller queued behind the same
// lock, and the invocation timed out. So the group is opened in the background
// and a call that arrives before it exists is refused by name.
func (d *databases) groupFor(namespace string) (raft.Cluster, error) {
	if d.node.opts.raft == nil {
		return nil, ErrNoRaft
	}

	d.groupsMu.Lock()
	if d.groupsGone {
		d.groupsMu.Unlock()
		return nil, databaseService.ErrGroupClosed
	}
	g, opening := d.groups[namespace]
	if !opening {
		g = &group{forming: make(chan struct{})}
		if d.groups == nil {
			d.groups = map[string]*group{}
		}
		d.groups[namespace] = g

		go func() {
			defer close(g.forming)
			cluster, err := d.node.opts.raft(namespace)

			d.groupsMu.Lock()
			defer d.groupsMu.Unlock()
			g.cluster, g.err = cluster, err
		}()
	}
	d.groupsMu.Unlock()

	select {
	case <-g.forming:
	default:
		return nil, databaseService.ErrGroupForming
	}

	d.groupsMu.Lock()
	defer d.groupsMu.Unlock()
	switch {
	case g.err != nil:
		// AN OPEN THAT FAILED IS NOT A LEDGER THAT IS OVER. raft.New spends
		// thirty seconds looking for peers and gives up on whatever the disk,
		// the host or the network was doing at the time; leaving the failure in
		// the map would make one bad minute permanent for that database on this
		// node. The entry goes, so the NEXT call starts a fresh attempt.
		//
		// AND THE REASON TRAVELS RATHER THAN "STILL FORMING", which is what an
		// operator needs: the one that retries forever is the refusal that says
		// nothing about why.
		delete(d.groups, namespace)
		return nil, fmt.Errorf("node: opening %s: %w", namespace, g.err)
	case g.cluster == nil:
		// Closed underneath us, which is a node on its way out. It is NOT
		// "still forming": a caller told that retries here for a group that
		// will never form, and what it should do is ask another voter.
		return nil, databaseService.ErrGroupClosed
	}
	return g.cluster, nil
}

// raftStore is `consensus: raft`: a database.Store over one raft group.
//
// # A follower refuses rather than answering stale
//
// pkg/raft has no conditional write and Get reads APPLIED state, which lags on
// a follower and lags on a leader that has just won an election. So both halves
// are gated:
//
//   - a write is Set/Delete, which raft refuses off the leader anyway. The
//     refusal is made here first, and carries WHO leads, so the caller is told
//     what happened rather than reading it out of a transport error.
//   - a read takes a Barrier first, because "the value I just wrote" is the one
//     question a ledger's reader always asks, and applied state without a
//     barrier can be older than the write that already returned.
//
// THE BARRIER IS ALSO THE LEADERSHIP PROOF, which is the half that is not about
// lag. A partitioned leader answers IsLeader true until its lease expires and
// its state machine is stale — it would answer "no such key" for an entry that
// is committed. A barrier cannot commit without a quorum, so that node fails
// instead of lying.
//
// WHAT IT COSTS, SAID RATHER THAN HIDDEN: hashicorp/raft's Barrier appends a
// log entry, so a read-heavy ledger grows its log and its snapshot churn at
// READ rate. The cheaper correct rule is VerifyLeader — a quorum heartbeat with
// no log entry — paired with a wait on the applied index; pkg/raft's Cluster
// exposes neither, and hand-rolling a leadership check here would be wrong in
// exactly the window it exists for. That is the follow-up, and it is a change
// to pkg/raft rather than to this file.
//
// THIS STORE NEVER FORWARDS. A store that dialled would be a second write path
// with its own failure modes, reaching back up through the service that
// composed it — node/queue.go's rule. What it does instead is name the leader,
// which is what lets services/database send the one operation there.
type raftStore struct {
	group   func() (raft.Cluster, error)
	timeout time.Duration
}

var _ database.Store = (*raftStore)(nil)

func (s *raftStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	cluster, err := s.current(ctx)
	if err != nil {
		return nil, false, err
	}
	value, held := cluster.Get(key)
	if !held {
		return nil, false, missed(cluster)
	}
	return value, held, nil
}

func (s *raftStore) Put(ctx context.Context, key string, value []byte) error {
	cluster, budget, err := s.leading(ctx)
	if err != nil {
		return err
	}
	if err := cluster.Set(key, value, budget); err != nil {
		return s.wrote(key, err)
	}
	return nil
}

func (s *raftStore) Delete(ctx context.Context, key string) error {
	cluster, budget, err := s.leading(ctx)
	if err != nil {
		return err
	}
	if err := cluster.Delete(key, budget); err != nil {
		return s.wrote(key, err)
	}
	return nil
}

// missed is what an empty answer from the state machine means.
//
// A CLOSED CLUSTER AND A NIL STATE MACHINE BOTH ANSWER EMPTY, and the kv engine
// turns that into ErrNotFound — "no such key", which is a fact a ledger's
// reader acts on and the one answer this file must never invent. current()
// proves leadership BEFORE the read, and a node shutting down between the proof
// and the read would answer a miss for an entry that is committed. So the proof
// is made again after it: a node that no longer leads did not answer a miss, it
// did not answer.
func missed(cluster raft.Cluster) error {
	if cluster.IsLeader() {
		return nil
	}
	return databaseService.NotLeading{}
}

// List names every key under a prefix, with the full stored key — which is what
// database.Store owes and what the kv engine cuts its own prefix off.
//
// THE LEADERSHIP CHECK IS WHAT ERRORS, because Keys CANNOT. A closed cluster
// and a nil state machine both answer an empty slice, so a group that is gone
// and a ledger nobody has written to are the same answer — and "the ledger is
// empty" is a fact a caller acts on. The check is made on both sides of the
// read, because the group can go between them: see missed.
func (s *raftStore) List(ctx context.Context, prefix string) ([]string, error) {
	cluster, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	keys := cluster.Keys(prefix)
	if len(keys) == 0 {
		if err := missed(cluster); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// leading is this node's group, if this node may answer for it at all.
//
// A GROUP THAT HAS NOT ELECTED YET IS NOT A GROUP THAT REFUSED, and the
// difference reaches the caller: NotLeading with nobody named is "ask again",
// NotLeading with a peer is "ask that one". Neither waits. The deleted first
// version of this file waited one write timeout for an election, because
// refusing was all it could otherwise do; with somewhere to send the call there
// is something better to do than hold the caller.
func (s *raftStore) leading(ctx context.Context) (raft.Cluster, time.Duration, error) {
	budget, err := s.budget(ctx)
	if err != nil {
		return nil, 0, err
	}

	cluster, err := s.group()
	if err != nil {
		return nil, 0, err
	}
	if cluster.IsLeader() {
		return cluster, budget, nil
	}

	leader, err := cluster.Leader()
	if err != nil {
		return nil, 0, databaseService.NotLeading{}
	}
	return nil, 0, databaseService.NotLeading{Leader: leader.String()}
}

// budget is what one operation may cost: this store's ceiling, cut short by a
// caller that will not wait that long.
//
// THE CALLER'S DEADLINE IS THE CALLER'S, which pkg/raft takes as a duration and
// not as a context — so a cancelled caller would otherwise pay the full ten
// seconds for a commit nobody is still waiting for, on a request path where the
// gateway has already given up.
func (s *raftStore) budget(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	deadline, set := ctx.Deadline()
	if !set {
		return s.timeout, nil
	}
	left := time.Until(deadline)
	if left <= 0 {
		return 0, context.DeadlineExceeded
	}
	return min(left, s.timeout), nil
}

// current is what a read costs: leadership, and a barrier so applied state is
// everything committed.
//
// LEADING IS NOT THE SAME AS KNOWING — the sentence node/cron.go carries too. A
// node that has just won an election holds committed entries it has not
// applied, so a read without this can be older than a write that already
// returned to its caller.
func (s *raftStore) current(ctx context.Context) (raft.Cluster, error) {
	cluster, budget, err := s.leading(ctx)
	if err != nil {
		return nil, err
	}
	if err := cluster.Barrier(budget); err != nil {
		return nil, fmt.Errorf("node: reading %s behind a barrier: %w", cluster.Namespace(), err)
	}
	return cluster, nil
}

// wrote is what a refused write means.
//
// LEADERSHIP CAN GO BETWEEN THE CHECK AND THE APPLY, and when it does raft says
// so — which is the same sentence leading() makes and reaches the caller as the
// same arm, rather than as a store failure it would give up on.
func (s *raftStore) wrote(key string, err error) error {
	if errors.Is(err, raft.ErrNotLeader) {
		return databaseService.NotLeading{}
	}
	return fmt.Errorf("node: writing %s: %w", key, err)
}

// closeGroups releases every group this node opened, once each has finished
// opening — tearing one down mid-bootstrap would leave the goroutine writing
// into a closed store, which is node/queue.go's rule and the same failure.
func (d *databases) closeGroups() error {
	d.groupsMu.Lock()
	groups := d.groups
	d.groups = nil
	d.groupsGone = true
	d.groupsMu.Unlock()

	var err error
	for _, g := range groups {
		<-g.forming
		if g.cluster != nil {
			err = errors.Join(err, g.cluster.Close())
		}
	}
	return err
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
