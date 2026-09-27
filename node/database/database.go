// Package database backs the four data models the `database` kind declares, and
// keeps the copies of a database this node holds.
//
// # Why it is its own package
//
// Both halves are ADAPTERS. The first sits between the wasm tier, which knows a
// grant and four sets of verbs, and pkg/database, which knows a Spec and a
// registry of engines. The second sits between those engines, which ask for the
// store ONE database's entries live in, and two things that answer — a replicated
// kvdb the composition root opens, and a raft group node/ledger holds. The root
// is the only place that may NAME all of those — but naming them and implementing
// the adapters are different jobs, and conflating them is how node/ grew to eight
// thousand lines with twenty-two of these in one namespace.
//
// So the root hands these what they can ask. Everything this package needs is in
// its own signature, which is the property a type taking *Node does not have:
// that one can reach anything the node has, so nothing about it says what it
// actually depends on, and it cannot be built anywhere except beside the node.
//
// # Four data models, one registry, one connection per handle
//
// `database` is four models — kv, sql, document, vector — on one axis and two
// consensus resolutions on another. Which model a grant names decides which
// surface the connection has to answer; whichever it is, it is opened once and
// kept, because a pool is what database/sql already is and because a kv
// connection is a prefix over a store that is already open.
//
// # No driver is linked here, and that is pkg/database's decision
//
// database/sql is a registry: a driver registers itself from its own init, so a
// deployment that wants Postgres imports one and registers the engine —
//
//	import _ "github.com/jackc/pgx/v5/stdlib"
//	node.WithDatabaseEngines(reg)  // reg holds sql.Postgres("pgx")
//
// tau therefore links no database driver, which is what lets the same binary
// serve a cloud that has one and a cloud that does not. A node with nothing
// registered answers every database call by naming what IS registered, which is a
// message an operator can act on rather than a connection that hangs.
//
// tau's own engines are the exception and are registered HERE rather than by a
// composer: that is what makes `type: kv` work with nothing to provision — a
// project declares a database and gets one from the swarm it is already on. Which
// models those cover is [TauBacked].
//
// # The credential is resolved here and nowhere earlier
//
// The compiled resource carries a REFERENCE to a secret. It is resolved at the
// moment a connection is opened, through the same vault a workload reads a
// run-time variable from — so a password is never in a spec, which is logged,
// cached, compared and handed to a tier.
//
// # A store per database, which is a topic per database
//
// That is the point rather than a side effect — see database.Stores. One store
// for all of them meant every subscriber held every database's rows, so
// placement decided nothing, and a Delete was a tombstone every replica applied:
// dropping a copy this node no longer holds deleted the copy the node that just
// acquired it holds.
//
// # What it holds is read off the disk and not off the table
//
// A restarted node holds everything it held before and has opened none of it. A
// holder that answered from its warm table would say it holds nothing, and every
// move that needed it would either wait forever or, worse, count its "not held"
// as a reason for somebody else to let go.
//
// # And a released copy is not served from the table again
//
// Destroying a database closes the store it destroys. A node that kept that
// store in the table it serves from went on answering every later call out of a
// closed store — not-found for rows another node holds, and the CRDT's own
// bookkeeping written back under the prefix that was just removed, which makes
// this node advertise a database it does not have.
package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/database"
	docengine "github.com/taubyte/tau/pkg/database/engines/document"
	kvengine "github.com/taubyte/tau/pkg/database/engines/kv"
	vecengine "github.com/taubyte/tau/pkg/database/engines/vector"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
)

// Secrets resolves the secret a connection's credentials were declared as.
//
// ONE METHOD AND NOT A VAULT, whose other half is the developer's — Set, Delete
// and List, reached from the build frontend — and which also has a lifetime: a
// stream to whichever node holds the secrets service. Neither is a connection's
// business. What it needs is one read.
type Secrets interface {
	Secret(ctx context.Context, project, environment, name string) (string, error)
}

// Databases is the adapter the wasm tier opens a database through: one registry,
// one connection per handle, whichever data model the grant names. Construct it
// with New.
type Databases struct {
	registry *database.Registry
	stores   database.Stores
	secrets  Secrets

	mu   sync.Mutex
	open map[string]database.Conn
}

var (
	_ wasmtier.SQL            = (*Databases)(nil)
	_ wasmtier.KVs            = (*Databases)(nil)
	_ wasmtier.DocumentStores = (*Databases)(nil)
	_ wasmtier.VectorStores   = (*Databases)(nil)
)

// New wires the adapter.
//
// registry MAY BE NIL, and that is the ordinary build: nothing here links a
// driver, so a node whose composer registered no engine serves the three models
// tau backs itself and refuses the rest BY NAME rather than hanging on a
// connection nobody can make.
//
// stores and secrets may not be nil. Without somewhere to put entries tau's own
// engines cannot be registered at all, and without a way to read a secret a
// database declaring a credential would connect with an empty password and be
// refused by the server for the wrong reason.
func New(registry *database.Registry, stores database.Stores, secrets Secrets) (*Databases, error) {
	if stores == nil {
		return nil, errors.New("database: nowhere to keep the entries of the databases tau backs itself")
	}
	if secrets == nil {
		return nil, errors.New("database: no vault, so no declared credential can be resolved")
	}
	return &Databases{registry: registry, stores: stores, secrets: secrets}, nil
}

var (
	// ErrNoEngines is a node asked for a database with no engine registered.
	ErrNoEngines = errors.New("database: no database engine is registered in this build")

	// ErrNotSQL is a database whose data model this tier does not serve yet.
	ErrNotSQL = errors.New("database: only the sql data model is served")

	// ErrConsensusUnserved is a ledger reached through this node's OWN engines
	// rather than through the database service.
	//
	// IT IS REFUSED BY NAME AND NEVER MERGED. These engines are the fallback
	// for a node that runs no database service and can reach none, and a raft
	// database has nothing to keep its order in there: no group, no owner, and
	// nowhere to send a write that this node may not make. Serving it as crdt
	// would resolve two entries into one with no error to anybody, which is the
	// one thing the declaration exists to prevent.
	ErrConsensusUnserved = errors.New("database: a raft database is kept by the database service and not by this node's own engines")
)

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

// Query runs a statement that reads.
func (d *Databases) Query(ctx context.Context, grant invoke.Grant, statement string, args []any) (wasmtier.Rows, error) {
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
func (d *Databases) Exec(ctx context.Context, grant invoke.Grant, statement string, args []any) (wasmtier.Outcome, error) {
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
func (d *Databases) conn(ctx context.Context, grant invoke.Grant) (database.SQLConn, error) {
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
func (d *Databases) OpenKV(ctx context.Context, grant invoke.Grant) (wasmtier.KV, error) {
	opened, err := d.openFor(ctx, grant, database.KV)
	if err != nil {
		return nil, err
	}

	conn, ok := opened.(database.KVConn)
	if !ok {
		return nil, fmt.Errorf("database: %s answers no kv surface", grant.Settings["kv/engine"])
	}
	return keyValue{conn}, nil
}

// OpenDocuments is the document half of this backing.
func (d *Databases) OpenDocuments(ctx context.Context, grant invoke.Grant) (wasmtier.Documents, error) {
	opened, err := d.openFor(ctx, grant, database.Document)
	if err != nil {
		return nil, err
	}

	conn, ok := opened.(database.DocumentConn)
	if !ok {
		return nil, fmt.Errorf("database: %s answers no document surface", grant.Settings["document/engine"])
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
func (d *Databases) OpenVectors(ctx context.Context, grant invoke.Grant) (wasmtier.Vectors, error) {
	opened, err := d.openFor(ctx, grant, database.Vector)
	if err != nil {
		return nil, err
	}

	conn, ok := opened.(database.VectorConn)
	if !ok {
		return nil, fmt.Errorf("database: %s answers no vector surface", grant.Settings["vector/engine"])
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

// openFor resolves one grant into a connection of the given data model, and
// keeps it.
//
// A CONNECTION IS KEPT BECAUSE A POOL IS WHAT database/sql ALREADY IS, and
// because a kv connection is a prefix over a store that is already open —
// re-deriving either per invocation would be work for no answer.
func (d *Databases) openFor(ctx context.Context, grant invoke.Grant, want database.Type) (database.Conn, error) {
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
		return nil, fmt.Errorf("database: opening %s %q: %w", spec.Engine, grant.Label, err)
	}

	if d.open == nil {
		d.open = map[string]database.Conn{}
	}
	d.open[grant.Handle] = opened
	return opened, nil
}

// specOf turns a grant into what pkg/database opens from.
func (d *Databases) specOf(ctx context.Context, grant invoke.Grant, want database.Type) (database.Spec, error) {
	model := grant.Settings[settingType]
	if database.Type(model) != want {
		return database.Spec{}, fmt.Errorf("%w: %q is %q", ErrNotSQL, grant.Label, model)
	}

	project, environment, id := specs.SplitHandle(grant.Handle)
	spec := database.Spec{
		Type:   want,
		Engine: grant.Settings[model+"/"+settingEngine],

		Project:     project,
		Environment: environment,

		// THE RESOLVED ID AND NOT THE AUTHOR'S LABEL. A label is local to the
		// resource that wrote it — two workloads may call one database
		// different things, and two different databases may both be called
		// `tokens` — so a kv engine keying its rows on the label would put two
		// databases in one key space. It is the id for every engine and not
		// just that one, because Spec.Name is what ADDRESSES the resource.
		Name:     keyedAs(id),
		Address:  grant.Settings[model+"/"+settingHost],
		Database: grant.Settings[model+"/"+settingDatabase],

		// WHAT THE AUTHOR DECLARED ABOUT CONCURRENT WRITES, carried by
		// services/worker onto the grant. Reading it is what lets the refusal
		// below be by name.
		Consensus: grant.Settings[model+"/"+settingConsensus],
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
	spec.Credentials.TLS = grant.Settings[model+"/"+settingTLS] != "disable"

	// PER-TYPE DECLARATIONS TRAVEL AS OPTIONS. A vector's width and metric mean
	// nothing to the other three models, which is why they are a map here and
	// not fields on the Spec.
	for _, option := range []string{settingDimensions, settingMetric} {
		if value := grant.Settings[model+"/"+option]; value != "" {
			if spec.Options == nil {
				spec.Options = map[string]string{}
			}
			spec.Options[option] = value
		}
	}

	if reference := grant.Settings[model+"/"+settingCredentials]; reference != "" {
		username, password, err := d.credentials(ctx, project, environment, reference)
		if err != nil {
			return database.Spec{}, err
		}
		spec.Credentials.Username, spec.Credentials.Password = username, password
	}
	return spec, nil
}

// keyedAs is the id an engine keys its rows under.
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
func keyedAs(id string) string { return strings.ReplaceAll(id, "/", ":") }

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
func (d *Databases) engines(ctx context.Context, want database.Type) (*database.Registry, error) {
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

	var engine database.Engine
	var err error
	switch want {
	case database.KV:
		engine, err = kvengine.New(d.stores)
	case database.Document:
		engine, err = docengine.New(d.stores)
	default:
		engine, err = vecengine.New(d.stores)
	}
	if err != nil {
		return nil, err
	}
	if err := d.registry.Register(ctx, engine); err != nil {
		return nil, fmt.Errorf("database: registering tau's own %s engine: %w", want, err)
	}
	return d.registry, nil
}

// The setting keys services/worker writes, under the model.
const (
	settingType       = "type"
	settingEngine     = "engine"
	settingConsensus  = "consensus"
	settingHost       = "host"
	settingDatabase   = "database"
	settingTLS        = "tls"
	settingDimensions = "dimensions"
	settingMetric     = "metric"
	// UNDER THE MODEL, as `sql/credentials/name` — which is where the vocabulary
	// declares it and where every other connection field already sat. A bare
	// `credentials/name` is a key `database` does not have.
	settingCredentials = "credentials/name"
)

// Settings is every attribute path this adapter reads off a grant.
//
// IT IS EXPORTED SO THE ROOT CAN HOLD IT TO THE VOCABULARY. A renamed attribute
// does not fail here: the key is absent, the read is a zero value, and a declared
// host silently becomes no host, a declared width no width, and `tls: disable`
// the state an author has to write a word to reach. node/settings_test.go walks
// this list against the schema, and its own doc carries the post-mortem that test
// came out of.
//
// IT IS WRITTEN PER MODEL because the vocabulary declares it per model, and the
// reads above are not: they compose `<model>/<key>` for whichever model the grant
// names, so `kv/host` is asked for and is a key `database` does not declare. That
// costs nothing — an absent key is an empty string and tau's own engines want no
// address, no credential and no tls — but it is why this list cannot be derived
// from what the code asks for, and why only what IS declared is listed.
var Settings = []string{
	settingType,

	// EVERY MODEL declares which engine serves it and how concurrent writes to
	// one key are ordered.
	"sql/" + settingEngine, "kv/" + settingEngine,
	"document/" + settingEngine, "vector/" + settingEngine,
	"sql/" + settingConsensus, "kv/" + settingConsensus,
	"document/" + settingConsensus, "vector/" + settingConsensus,

	// THE TWO THAT REACH A SERVER SOMEBODY ELSE RUNS carry where it is, which
	// database on it, whether the connection is verified, and the secret to
	// authenticate with. tau's own engines declare none of those, which is the
	// whole difference between declaring a database and provisioning one.
	"sql/" + settingHost, "sql/" + settingDatabase,
	"sql/" + settingTLS, "sql/" + settingCredentials,
	"document/" + settingHost, "document/" + settingDatabase,
	"document/" + settingTLS, "document/" + settingCredentials,

	// AND A WIDTH AND A METRIC ARE A VECTOR'S ALONE, which is why they travel as
	// options rather than as fields on the Spec.
	"vector/" + settingDimensions, "vector/" + settingMetric,
}

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
func (d *Databases) credentials(ctx context.Context, project, environment, reference string) (username, password string, err error) {
	value, err := d.secrets.Secret(ctx, project, environment, reference)
	if err != nil {
		return "", "", fmt.Errorf("database: reading the credentials for %s/%s: %w", project, environment, err)
	}

	if user, pass, found := strings.Cut(value, ":"); found {
		return user, pass, nil
	}
	return "", value, nil
}

// Close releases every connection this backing opened.
func (d *Databases) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var err error
	for _, conn := range d.open {
		err = errors.Join(err, conn.Close())
	}
	d.open = nil
	return err
}
