package wasm

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// host.go is the register of capabilities this tier serves, one row per world under
// wit/deps; adding a capability is one row and one file. A component's imports
// name only what it calls, but wazy cannot decode them without instantiating
// (.notes/v2/wazy-request.md), so every capability is registered for every
// workload. Registering even ungranted ones matters: a missing import traps and
// kills the instance, while a registered one can refuse per call.

// hostComponent is one capability's host half.
type hostComponent struct {
	// name is the capability as the vocabulary spells it, for a message.
	name string

	// iface is the WIT interface this row serves, as an import section spells it;
	// host_test.go holds the register to wit/handler.wit both ways.
	iface string

	// reaches is the grants this tier must serve; refused grants don't count.
	reaches func(invoke.Spec) map[string]invoke.Grant

	// backed is whether this tier was composed with a backing, a bool because a typed
	// nil in an interface is not nil.
	backed func(*Runtime) bool

	// missing is what a granted capability with no backing is refused with.
	missing error

	// register is the imports for one instantiation.
	register func(*Runtime, invoke.Spec) []component.Option
}

// hostComponents is every capability served, in the order a guest meets them; a
// function so `backed` closures read the Runtime asked about.
func hostComponents() []hostComponent {
	return []hostComponent{
		{
			name:     "cache",
			iface:    cacheInterface,
			reaches:  grantsOf,
			backed:   func(r *Runtime) bool { return r.caches != nil },
			missing:  ErrNoCaches,
			register: (*Runtime).cacheImports,
		},
		{
			// `sql` is the capability; `database` is the KIND, which has four
			// of them. See wit/deps/taubyte-sql.
			name:     "sql",
			iface:    sqlInterface,
			reaches:  sqlGrantsOf,
			backed:   func(r *Runtime) bool { return r.sqlStore != nil },
			missing:  ErrNoSQL,
			register: (*Runtime).sqlImports,
		},
		{
			name:     "kv",
			iface:    kvInterface,
			reaches:  kvGrantsOf,
			backed:   func(r *Runtime) bool { return r.kv != nil },
			missing:  ErrNoKV,
			register: (*Runtime).kvImports,
		},
		{
			name:     "document",
			iface:    documentInterface,
			reaches:  documentGrantsOf,
			backed:   func(r *Runtime) bool { return r.documents != nil },
			missing:  ErrNoDocuments,
			register: (*Runtime).documentImports,
		},
		{
			name:     "vector",
			iface:    vectorInterface,
			reaches:  vectorGrantsOf,
			backed:   func(r *Runtime) bool { return r.vectors != nil },
			missing:  ErrNoVectors,
			register: (*Runtime).vectorImports,
		},
		{
			name:     "storage",
			iface:    storageInterface,
			reaches:  servableStorage,
			backed:   func(r *Runtime) bool { return r.storage != nil },
			missing:  ErrNoStorage,
			register: (*Runtime).storageImports,
		},
		{
			name:     "stream",
			iface:    streamInterface,
			reaches:  streamGrantsOf,
			backed:   func(r *Runtime) bool { return r.streams != nil },
			missing:  ErrNoStreams,
			register: (*Runtime).streamImports,
		},
		{
			name:     "bus",
			iface:    busInterface,
			reaches:  busGrantsOf,
			backed:   func(r *Runtime) bool { return r.bus != nil },
			missing:  ErrNoBus,
			register: (*Runtime).busImports,
		},
		{
			name:     "queue",
			iface:    queueInterface,
			reaches:  queueGrantsOf,
			backed:   func(r *Runtime) bool { return r.queues != nil },
			missing:  ErrNoQueues,
			register: (*Runtime).queueImports,
		},
		{
			// `log` is the `sync` kind's other type: a queue hands work over,
			// a log keeps history. Different verbs, different backing.
			name:     "log",
			iface:    logInterface,
			reaches:  logGrantsOf,
			backed:   func(r *Runtime) bool { return r.logs != nil },
			missing:  ErrNoLogs,
			register: (*Runtime).logImports,
		},
		{
			// A workflow is STARTED by sending to the first step's queue, so
			// what backs it is the queues — which is why this row's backing is
			// not a field of its own.
			name:     "workflow",
			iface:    workflowInterface,
			reaches:  servableWorkflows,
			backed:   func(r *Runtime) bool { return r.queues != nil },
			missing:  ErrNoWorkflows,
			register: (*Runtime).workflowImports,
		},
		{
			name:     "channel",
			iface:    channelInterface,
			reaches:  servableChannels,
			backed:   func(r *Runtime) bool { return r.channels != nil },
			missing:  ErrNoChannels,
			register: (*Runtime).channelImports,
		},
		{
			name:     "secret",
			iface:    secretInterface,
			reaches:  secretGrantsOf,
			backed:   func(r *Runtime) bool { return r.secrets != nil },
			missing:  ErrNoSecrets,
			register: (*Runtime).secretImports,
		},
		{
			name:     "model",
			iface:    modelInterface,
			reaches:  modelGrantsOf,
			backed:   func(r *Runtime) bool { return r.inference != nil },
			missing:  ErrNoInference,
			register: (*Runtime).modelImports,
		},
		{
			// What the workload is: authority over nothing, so always registered and never
			// refused.
			name:     "workload",
			iface:    workloadInterface,
			register: (*Runtime).workloadImports,
		},
	}
}

// imports is every capability's registration for one instantiation; a granted
// capability with no backing is refused at Start rather than failing per request.
func (r *Runtime) imports(spec invoke.Spec) ([]component.Option, error) {
	var out []component.Option
	for _, c := range hostComponents() {
		if c.reaches != nil && len(c.reaches(spec)) > 0 && !c.backed(r) {
			return nil, fmt.Errorf("%w: %s", c.missing, spec.Name)
		}
		out = append(out, c.register(r, spec)...)
	}
	return out, nil
}

// unserved warns about every declared capability no row here answers. The composer
// passes every kind's grant so a tier can see what it cannot do; without this a
// guest would fail at the call with no word at start. A warning, as in the
// container tier; mounts are refused instead (ErrNoMounts).
func (r *Runtime) unserved(spec invoke.Spec) {
	if r.warn == nil {
		return
	}

	served := map[string]bool{}
	for _, c := range hostComponents() {
		if c.reaches == nil {
			continue
		}
		for _, g := range c.reaches(spec) {
			served[g.Kind+"/"+g.Label] = true
		}
	}

	missing := map[string]bool{}
	for _, g := range spec.Reaches {
		if g.Kind == invoke.MountKind || served[g.Kind+"/"+g.Label] {
			continue
		}
		missing[g.Kind] = true
	}
	if len(missing) == 0 {
		return
	}

	kinds := make([]string, 0, len(missing))
	for kind := range missing {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)

	r.warn("wasm: %s declares access this tier has no host interface for (%s); "+
		"nothing is imported for them and the guest will fail where it calls one",
		spec.Name, strings.Join(kinds, ", "))
}
