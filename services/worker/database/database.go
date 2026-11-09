// Package database backs the `database` kind's four data models and keeps the copies
// this node holds. Two adapters: one between the wasm tier and pkg/database's
// registry, one between the engines' store requests and the replicated kvdb or
// instance/ledger's raft groups.
//
// Each grant's connection is opened once and kept. No driver is linked here
// (pkg/database's decision): the worker's section names the external engines, and
// the binary links their drivers (cmd/taud, instance/dream).
//
// tau's own engines are registered here, so `type: kv` needs nothing provisioned (see
// TauBacked). A credential reference is resolved through the vault only when a
// connection opens, so no password is ever in a spec.
//
// One store per database (see database.Stores). Holdings are read off disk, since a
// restarted node has opened nothing, and a released copy's closed store is dropped
// from the table so it is never served again.
package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
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

// Secrets resolves the secret a connection's credentials were declared as: one read,
// not the vault.
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

// New wires the adapter. registry may be nil (no driver linked): tau's models are
// served and the rest refused by name. stores and secrets may not be nil.
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

	// ErrConsensusUnserved is a raft database reached through this node's fallback
	// engines, which have no group to keep its order; refused, never merged.
	ErrConsensusUnserved = errors.New("database: a raft database is kept by the database service and not by this node's own engines")
)

// TauBacked is every data model this node opens against the swarm's own store,
// rather than against a server somebody else runs.
//
// EXPORTED SO THE VOCABULARY CAN BE HELD TO IT. schema.DatabaseEngines() offers
// tau's own engine for EVERY model, `sql` included. sql is not here because it
// is not the swarm's store: it is a musql file per database, and the node
// registers that engine into the registry its section's external engines are in
// (services/worker/hosted/sql.go), which is where a workload's `sql` access is
// answered from.
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
		return nil, fmt.Errorf("%w: %s answers no sql surface", ErrNotSQL, grant.Settings[specs.DatabaseSqlKeyEngine])
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
		return nil, fmt.Errorf("database: %s answers no kv surface", grant.Settings[specs.DatabaseKvKeyEngine])
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
		return nil, fmt.Errorf("database: %s answers no document surface", grant.Settings[specs.DatabaseDocumentKeyEngine])
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
// same job instance/cache.go does and the same bug it was written to stop.
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
		return nil, fmt.Errorf("database: %s answers no vector surface", grant.Settings[specs.DatabaseVectorKeyEngine])
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
		// The tier's miss, not the engine's, or every miss reads as an outage.
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
	keys := keysOf[model]

	project, environment, id := specs.SplitHandle(grant.Handle)
	spec := database.Spec{
		Type:   want,
		Engine: grant.Settings[keys.engine],

		Project:     project,
		Environment: environment,

		// The resolved id, not the author's label: labels are per workload and not unique,
		// and Spec.Name addresses the resource.
		Name:     keyedAs(id),
		Address:  grant.Settings[keys.host],
		Database: grant.Settings[keys.database],

		// WHAT THE AUTHOR DECLARED ABOUT CONCURRENT WRITES, carried by
		// services/worker onto the grant. Reading it is what lets the refusal
		// below be by name.
		Consensus: grant.Settings[keys.consensus],
	}
	if raw := grant.Settings[keys.size]; raw != "" {
		size, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return database.Spec{}, fmt.Errorf("database: %q declares a size %q that is not a number of bytes", grant.Label, raw)
		}
		spec.Size = size
	}

	if spec.Consensus == database.Raft {
		return database.Spec{}, fmt.Errorf("%w: %s", ErrConsensusUnserved, grant.Label)
	}

	// TLS is on unless the author wrote `disable`. `require` is served as verify-full:
	// pkg/database carries a bool, and the weaker reading would accept a network
	// attacker as the server.
	spec.Credentials.TLS = keys.tls == "" || grant.Settings[keys.tls] != keys.disabled

	// PER-TYPE DECLARATIONS TRAVEL AS OPTIONS. A vector's width and metric mean
	// nothing to the other three models, which is why they are a map here and
	// not fields on the Spec.
	for option, key := range map[string]string{vecengine.OptionDimensions: keys.dimensions, vecengine.OptionMetric: keys.metric} {
		if value := grant.Settings[key]; key != "" && value != "" {
			if spec.Options == nil {
				spec.Options = map[string]string{}
			}
			spec.Options[option] = value
		}
	}

	if reference := grant.Settings[keys.credentials]; keys.credentials != "" && reference != "" {
		username, password, err := d.credentials(ctx, project, environment, reference)
		if err != nil {
			return database.Spec{}, err
		}
		spec.Credentials.Username, spec.Credentials.Password = username, password
	}
	return spec, nil
}

// keyedAs is the id an engine keys its rows under. An admitted instance makes the
// handle `<project>/<env>/<id>/<instance>`, and pkg/database refuses a name with "/",
// so the separator is folded to ":" here. A CID has no ":" and an instance name no
// "/", so the mapping is injective.
func keyedAs(id string) string { return strings.ReplaceAll(id, "/", ":") }

// engines is the registry this node serves from, with tau's own engines registered
// on first use so a declared kv needs nothing provisioned. Other engines are the ones
// the worker's section names.
func (d *Databases) engines(ctx context.Context, want database.Type) (*database.Registry, error) {
	// Only the models tau backs (TauBacked); sql is not the swarm's store.
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

// The setting keys services/worker writes, under the model. Each is the
// vocabulary's own attribute path, so the generated constants are the keys.
const settingType = specs.DatabaseKeyType

// modelKeys is where one data model's connection fields travel. A field the
// model does not declare has no key, and reads as absent.
type modelKeys struct {
	engine, consensus, size          string
	host, database, tls, credentials string
	dimensions, metric               string

	// disabled is the model's own word for turning tls off.
	disabled string
}

// keysOf is every model's keys. IT IS WRITTEN PER MODEL because the vocabulary
// declares them per model: tau's own engines want no address, no credential
// and no tls, and a width and a metric are a vector's alone.
var keysOf = map[string]modelKeys{
	specs.DatabaseTypeSql: {
		engine: specs.DatabaseSqlKeyEngine, consensus: specs.DatabaseSqlKeyConsensus, size: specs.DatabaseSqlKeySize,
		host: specs.DatabaseSqlKeyHost, database: specs.DatabaseSqlKeyDatabase,
		tls: specs.DatabaseSqlKeyTls, disabled: specs.DatabaseSqlTlsDisable,
		credentials: specs.DatabaseSqlKeyCredentialsName,
	},
	specs.DatabaseTypeDocument: {
		engine: specs.DatabaseDocumentKeyEngine, consensus: specs.DatabaseDocumentKeyConsensus, size: specs.DatabaseDocumentKeySize,
		host: specs.DatabaseDocumentKeyHost, database: specs.DatabaseDocumentKeyDatabase,
		tls: specs.DatabaseDocumentKeyTls, disabled: specs.DatabaseDocumentTlsDisable,
		credentials: specs.DatabaseDocumentKeyCredentialsName,
	},
	specs.DatabaseTypeKv: {
		engine: specs.DatabaseKvKeyEngine, consensus: specs.DatabaseKvKeyConsensus, size: specs.DatabaseKvKeySize,
	},
	specs.DatabaseTypeVector: {
		engine: specs.DatabaseVectorKeyEngine, consensus: specs.DatabaseVectorKeyConsensus, size: specs.DatabaseVectorKeySize,
		dimensions: specs.DatabaseVectorKeyDimensions, metric: specs.DatabaseVectorKeyMetric,
	},
}

// Settings is every attribute path this adapter reads off a grant, exported so
// instance/settings_test.go can hold it to the vocabulary: a renamed attribute would
// silently drop a host, a width, or `tls: disable`.
var Settings = func() []string {
	out := []string{settingType}
	for _, keys := range keysOf {
		for _, key := range []string{keys.engine, keys.consensus, keys.size, keys.host, keys.database, keys.tls, keys.credentials, keys.dimensions, keys.metric} {
			if key != "" {
				out = append(out, key)
			}
		}
	}
	sort.Strings(out)
	return out
}()

// credentials reads the secret the configuration referenced, as "username:password"
// split on the first colon (a URL's userinfo shape); no colon is a password alone.
// A convention, since the vocabulary does not say.
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
