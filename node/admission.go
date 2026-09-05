package node

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	ds "github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	gateway "github.com/taubyte/tau/services/v2/gateway"
	"github.com/taubyte/tau/services/worker"
)

// admission.go decides whether one instance of a store may exist.
//
// # The validator is a workload, so this is where it can be called from
//
// The vocabulary's `instances:` block names a FUNCTION that admits an instance,
// which the wasm tier cannot invoke: it runs one workload and has no way to
// reach another. The node has both — the tier it composed and the worker that
// invokes — so this is the one place the two meet, the same reason every other
// backing is here.
//
// # An admitted instance is remembered, because a validator is a request
//
// Asking on every call would put an invocation in front of every get, which is
// a store nobody would use. So a decision is recorded, and what records it is a
// replicated kvdb row: any node may admit, every node sees it, and the row is
// the same value whoever wrote it — which is the rule AGENTS.md states for
// every kvdb layout here.
//
// # The ceiling is eventually exact, and that is a real limit
//
// `instances/limit` is counted by scanning what has been admitted. Under a CRDT
// two nodes admitting at the same moment can both see room, so a limit may be
// exceeded by however many admissions raced — and then it holds. THE ALTERNATIVE
// IS A COUNTER, which is precisely the key a kvdb must never hold: a value two
// nodes write differently. The validator is the control that decides WHICH
// instances exist; the limit is a ceiling against runaway, and a ceiling that
// is occasionally one over is still a ceiling.
//
// A limit of 0 is "no ceiling", which the vocabulary makes a required sentence
// rather than a default nobody reached.
//
// # What the validator is asked, and what it answers with
//
// It is invoked as a handler — `POST /` with the instance and the store in
// headers — and a 2xx admits. THAT IS NOT THE WORLD THE CONFIG NAMES: the
// vocabulary's `implements: instance-validator` is one of seven host worlds and
// only `handler` exists, so a function written today IS a handler whatever its
// config says. When that world lands, this dispatch is what changes, and the
// keyword already says which functions are affected.
//
// What a validator RETURNS beyond yes or no — an instance's ttl, its size — is
// deliberately not modelled here, for the vocabulary's own reason: config
// declares which function decides and the parameters are the decision's.

// admissions is the node's Admitter.
type admissions struct {
	node *Node

	mu sync.Mutex
	db kvdb.KVDB

	// seen is what this node has already admitted, so a hot instance costs
	// nothing after the first open. It is a cache of the row and not the
	// authority: a node that has not seen one reads the store.
	seen map[string]bool
}

var (
	_ wasmtier.Admitter = (*admissions)(nil)
	_ gateway.Admits    = (*admissions)(nil)
)

// AdmitInstance is the gateway's Admits: the same decision, asked about a
// BROWSER rather than a workload.
//
// IT IS ONE IMPLEMENTATION AND NOT TWO, which is the whole point. A workload
// opening `chat/room-42` and a browser attaching to `/room-42` are asking the
// same question of the same validator about the same instance, and two answers
// to it would be two security policies for one config block.
func (a *admissions) AdmitInstance(ctx context.Context, handle string, settings map[string]string, instance string) error {
	return a.admit(ctx, invoke.Grant{
		Kind: "bus", Label: labelOf(handle), Handle: handle, Settings: settings,
	}, instance)
}

// labelOf is something to name the store by in a message. The handle carries
// the resolved id and a browser never saw the author's label, so the id is what
// there is — and it is better than nothing in a log.
func labelOf(handle string) string {
	_, _, id := splitHandle(handle)
	return id
}

// ErrNoValidator is a store that declares an admission this node cannot run —
// an `instances:` block naming no function, or a node with no worker.
var ErrNoValidator = errors.New("node: this store admits instances through a function and this node runs no worker")

// admissionDatabase is the one database's name, for cacheDatabase's reason.
const admissionDatabase = "instances"

// The headers a validator is told about the instance it is deciding on. A
// workload deciding for two stores has nothing else to tell them apart by.
const (
	headerInstance = "X-Taubyte-Instance"
	headerFor      = "X-Taubyte-Instance-Of"
)

// Admit runs the target's validator, once per instance, and holds the ceiling.
// It is what the wasm tier asks.
func (a *admissions) Admit(ctx context.Context, grant invoke.Grant, instance string) error {
	return a.admit(ctx, grant, instance)
}

// admit is the decision, whoever asked.
func (a *admissions) admit(ctx context.Context, grant invoke.Grant, instance string) error {
	// AN INSTANCE NAME IS A KEY AND THE STORE PATH-CLEANS ONE. `..` is not a
	// name that misses — it RESOLVES, to `<project>/<environment>`, so admitting
	// it would write one row that every other store in the deployment then reads
	// as its own admission and every instance of every one of them is admitted
	// without a validator ever being asked.
	//
	// The guard is here rather than in each caller because both meet here: the
	// wasm tier refuses a "/" already, and the gateway takes the instance from
	// a URL path where "." and ".." are ordinary segments.
	if err := singleSegment(instance); err != nil {
		return err
	}

	key := grant.Handle + "/" + instance

	a.mu.Lock()
	known := a.seen[key]
	a.mu.Unlock()
	if known {
		return nil
	}

	db, err := a.store()
	if err != nil {
		return err
	}

	// ALREADY ADMITTED SOMEWHERE. The row is the decision and it replicates, so
	// a validator is asked once per instance per cloud rather than once per
	// node — which matters because a validator may have side effects.
	if _, err := db.Get(ctx, key); err == nil {
		a.remember(key)
		return nil
	} else if !errors.Is(err, ds.ErrNotFound) {
		return err
	}

	if err := a.room(ctx, db, grant); err != nil {
		return err
	}
	if err := a.ask(ctx, grant, instance); err != nil {
		return err
	}

	// THE ROW IS A MARKER AND NOT A RECORD OF THE DECISION'S PARAMETERS. What a
	// validator returns beyond yes is not modelled, so writing it here would be
	// inventing a shape; what is written is that this instance exists, which is
	// what the ceiling counts and what the next open reads.
	if err := db.Put(ctx, key, []byte(instance)); err != nil {
		return err
	}
	a.remember(key)
	return nil
}

// singleSegment refuses an instance name that is not one clean path segment.
func singleSegment(instance string) error {
	if instance == "" || instance == "." || instance == ".." || strings.Contains(instance, "/") {
		return fmt.Errorf("%w: %q is not an instance name", wasmtier.ErrNotAdmitted, instance)
	}
	return nil
}

// room refuses an admission that would take the store past its ceiling.
func (a *admissions) room(ctx context.Context, db kvdb.KVDB, grant invoke.Grant) error {
	limit, err := strconv.ParseInt(grant.Settings[settingInstanceLimit], 10, 64)
	if err != nil || limit <= 0 {
		// Unparseable is treated as absent, and absent is no ceiling — the
		// vocabulary requires the field, so a missing one means the composer
		// did not read it, and refusing every instance over a field nobody
		// wrote would be an outage where this is a bug to find in a log.
		return nil
	}

	held, err := db.List(ctx, grant.Handle+"/")
	if err != nil {
		return err
	}
	if int64(len(held)) >= limit {
		return fmt.Errorf("%w: %s holds %d instances and its limit is %d",
			wasmtier.ErrNotAdmitted, grant.Label, len(held), limit)
	}
	return nil
}

// ask invokes the validator the target named.
func (a *admissions) ask(ctx context.Context, grant invoke.Grant, instance string) error {
	name := grant.Settings[settingInstanceValidator]
	if name == "" || a.node.worker == nil {
		return fmt.Errorf("%w: %s", ErrNoValidator, grant.Label)
	}

	project, environment, id := splitHandle(grant.Handle)
	if project == "" || environment == "" || id == "" {
		return fmt.Errorf("node: the handle %q does not name a deployment", grant.Handle)
	}

	res, err := a.node.worker.Invoke(ctx, worker.Target{
		Project:     project,
		Environment: environment,
		Scope:       splitNamespace(grant.Settings[settingInstanceScope]),
		Kind:        "function",
		Name:        name,
	}, &invoke.Request{
		Method: http.MethodPost,
		Path:   "/",
		Headers: map[string][]string{
			headerInstance: {instance},
			headerFor:      {grant.Label},
		},
	})
	if err != nil {
		// A VALIDATOR THAT COULD NOT BE RUN IS NOT A REFUSAL. Admitting anyway
		// would be granting what the target said somebody else decides; saying
		// "not admitted" would tell the caller their instance was turned down,
		// which is a different thing to act on.
		return fmt.Errorf("node: the validator %q for %s could not be run: %w", name, grant.Label, err)
	}
	if res.Status < 200 || res.Status > 299 {
		return fmt.Errorf("%w: %s refused %q with %d", wasmtier.ErrNotAdmitted, name, instance, res.Status)
	}
	return nil
}

func (a *admissions) remember(key string) {
	a.mu.Lock()
	if a.seen == nil {
		a.seen = map[string]bool{}
	}
	a.seen[key] = true
	a.mu.Unlock()
}

// store opens the one database, once.
func (a *admissions) store() (kvdb.KVDB, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.db != nil {
		return a.db, nil
	}
	if a.node.opts.databases == nil {
		return nil, ErrNoCacheStore
	}

	db, err := a.node.opts.databases.New(logger, admissionDatabase, rebroadcastSeconds)
	if err != nil {
		return nil, fmt.Errorf("node: opening the instances database: %w", err)
	}
	a.db = db
	a.node.closers = append(a.node.closers, closerFunc(db.Close))
	return db, nil
}

// Close forgets what this node had admitted. The rows stay: they are the
// decision and they are not this node's.
func (a *admissions) Close() error {
	a.mu.Lock()
	a.seen = nil
	a.mu.Unlock()
	return nil
}

// The setting keys services/worker writes for the `instances:` block.
const (
	settingInstanceAdmission = "instances/admission"
	settingInstanceLimit     = "instances/limit"
	settingInstanceValidator = "instances/validator/name"
	settingInstanceScope     = "instances/validator/namespace"
)

// splitNamespace turns "/shop/eu" into the scope chain a lookup takes. An empty
// one is the project root, which is what the vocabulary says an omitted
// namespace means.
func splitNamespace(namespace string) []string {
	namespace = strings.Trim(namespace, "/")
	if namespace == "" {
		return nil
	}
	return strings.Split(namespace, "/")
}
