package wasm

import (
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// host.go is the register of capabilities this tier can serve.
//
// # One host component per capability
//
// Each of the worlds under wit/deps is one entry here: what the vocabulary
// calls it, how to find a spec's grants of it, whether this tier was composed
// with anything to serve it, and the imports to register for one instantiation.
// Adding a capability is one row and one file; it is not an edit to Start.
//
// That matters beyond tidiness. Start used to check ten backings and append ten
// import sets in a hand-unrolled block whose order had drifted out of any
// pattern — a check for one capability sitting between the appends of two
// others — and the next two capabilities were going to be another four lines
// wedged into the same place. A capability is a thing; this is the list of them.
//
// # Only what is needed, and what still stands in the way
//
// MEASURED: a component's import section names only the interfaces it actually
// calls. `fixtures/handler.wasm` is built against the same world as
// `fixtures/cacher.wasm` and imports NONE of the taubyte interfaces, while the
// cacher imports all thirteen — so the artifact itself already says which host
// components it needs.
//
// This tier cannot read that yet: wazy exposes no way to decode a component's
// imports short of instantiating it, so every capability is registered for
// every workload. See .notes/v2/wazy-request.md, which asks for exactly the one
// function that closes it — at which point `register` gains a filter and
// nothing else here changes.
//
// # And they are registered even when nothing is granted
//
// An import a component declares and the host does not provide is a TRAP: the
// call panics inside wazy and takes the instance with it. So a workload calling
// a capability it never declared must still find a host function there, to be
// told `no-such-cache` and carry on. A guest bug is not a reason to lose warm
// state.
//
// The two rules meet cleanly: register what the ARTIFACT imports, refuse per
// call what the CONFIGURATION did not grant.

// hostComponent is one capability's host half.
type hostComponent struct {
	// name is the capability as the vocabulary spells it, for a message.
	name string

	// iface is the WIT interface this row serves, exactly as a component's
	// import section spells it.
	//
	// IT IS HERE SO THE TWO SIDES CAN BE COMPARED. host_test.go reads
	// wit/handler.wit and asserts this register covers every interface the
	// world imports and imports nothing it does not — which is the one mistake
	// this table exists to make impossible and could otherwise only be found by
	// a guest trapping in production.
	iface string

	// reaches is the grants of this capability that this tier would have to
	// serve. A grant the composer already refused does not count — see
	// servableStorage for why that distinction is not cosmetic.
	reaches func(invoke.Spec) map[string]invoke.Grant

	// backed is whether this tier was composed with something to serve it.
	//
	// IT ANSWERS A BOOL RATHER THAN THE BACKING because a nil *fakeCaches in an
	// interface field is not equal to nil, and a table handing back `any` would
	// need reflection to ask a question each row can answer in four characters.
	backed func(*Runtime) bool

	// missing is what a granted capability with no backing is refused with.
	missing error

	// register is the imports for one instantiation.
	register func(*Runtime, invoke.Spec) []component.Option
}

// hostComponents is every capability this tier serves, in the order a guest
// meets them: the data models first, then the pipes, then what a workload
// learns about itself.
//
// IT IS A FUNCTION AND NOT A PACKAGE VARIABLE so that a `backed` closure can
// read a field off the Runtime it is asked about rather than off a global.
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
			// WHAT THE WORKLOAD IS. It is authority over nothing, so there is
			// no grant to find and nothing to back it — a row with no `reaches`
			// is always registered and never refused. It is in the register
			// anyway because this is the list of capabilities, and one missing
			// from it is one nobody finds.
			name:     "workload",
			iface:    workloadInterface,
			register: (*Runtime).workloadImports,
		},
	}
}

// imports is every host component's registration for one instantiation, and the
// refusal for one that is granted with nothing behind it.
//
// A GRANT WITH NOTHING BEHIND IT IS A CONSTRUCTION FAULT, so it is refused at
// Start rather than at the first call: a workload that would fail every request
// touching its cache should not be started and then look healthy until somebody
// exercises that path.
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
