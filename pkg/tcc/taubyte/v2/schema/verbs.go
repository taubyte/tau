package schema

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// Ops declares what may be done TO this kind, on the kind itself. It is the only
// place a verb is written: the access block a resource authors, the enum an editor
// completes from, the JSON Schema, and the effect class a query groups by are all
// read back off this declaration, so none of them can drift from it.
//
//	DefineGroup("cache", resource(…,
//	    Ops(Read("get"), Read("list"), Read("watch"), Write("put"), Write("delete")),
//	))
//
// Effect classes are fixed at three — read, write, admin — and the verbs are
// per-kind, because a standard verb set breaks on the second resource type:
// function-to-function is `invoke`, which is neither get nor put, and collapsing
// publish/subscribe into `write` erases a real security boundary. The classes are
// the join key that keeps cross-cutting queries answerable.
const (
	Read  = "read"
	Write = "write"
	Admin = "admin"
)

type Verb struct {
	Name   string
	Effect string
	// Delivered marks a verb where the PLATFORM hands the work over, rather than the
	// resource going and asking for it. `get` on a cache is a call the workload
	// makes; `receive` on a queue and `subscribe` on a channel are the other
	// direction, and something has to actually arrive.
	//
	// It is what makes a consumer's inbound path expressible. A WASM consumer needs
	// nothing — the dispatch IS the delivery, and there is no port to speak of. A
	// CONTAINER has no host ABI to be called through, so the item has to be posted to
	// it, and this is the flag that puts a `deliver` block on those access entries and
	// on no others.
	Delivered bool

	// Mounted marks a verb that attaches a FILESYSTEM to the workload. Like Delivered
	// it exists because the verb alone is not the whole statement: "this container may
	// mount that volume" does not say where, and a database in a container is entirely
	// a question of where — /var/lib/postgresql/data is not a detail, it is the reason
	// the volume exists.
	Mounted bool
}

// Mounts marks a verb as attaching a filesystem, which gives its access entry a path.
func Mounts(v Verb) Verb { v.Mounted = true; return v }

// Read/Write/Admin name a verb and its effect class in one term, so a verb cannot be
// declared without one.
func ReadOp(name string) Verb  { return Verb{Name: name, Effect: Read} }
func WriteOp(name string) Verb { return Verb{Name: name, Effect: Write} }
func AdminOp(name string) Verb { return Verb{Name: name, Effect: Admin} }

// DeliveredOp is a read whose data ARRIVES. See Verb.Delivered.
func DeliveredOp(name string) Verb { return Verb{Name: name, Effect: Read, Delivered: true} }

// isMounted reports whether any of a kind's verbs attaches a filesystem.
func isMounted(kind string) bool {
	for _, v := range OpsOf(kind) {
		if v.Mounted {
			return true
		}
	}
	return false
}

// isDelivered reports whether any of a kind's verbs hands work over.
func isDelivered(kind string) bool {
	for _, v := range OpsOf(kind) {
		if v.Delivered {
			return true
		}
	}
	return false
}

// HTTP methods are NOT ops. They belong to a gateway's route, which is where a
// request enters and where the method is matched. Declaring them as a kind's ops
// would make a workload nameable as a direct target — a second door into it that no
// route knows about, with no path, no gateway policy and no name in front of it.
//
// Ops annotates a kind with its verb set.
func Ops(verbs ...Verb) NodeOption { return GroupAnnotate("ops", verbs) }

// OpsOf reads a kind's declared verbs back off the schema.
func OpsOf(kind string) []Verb {
	for _, g := range V2Resources {
		if name, _ := g.Match.(string); name != kind || len(g.Children) == 0 {
			continue
		}
		if v, ok := g.Children[0].Meta["ops"].([]Verb); ok {
			return v
		}
	}
	return nil
}

// ServedByRoute reports whether traffic can be handed to this kind — by a gateway route
// if it is HTTP, by an L4 proxy if it is a raw connection from outside, or by a stream
// if it is a raw connection from inside the project. It is the OTHER way to be
// reachable: ops are what one resource does to another, this is how a connection
// arrives. A kind with neither is not a target at all.
func ServedByRoute(kind string) bool {
	// No pluralisation to undo any more: a handler kind IS the kind's name. That
	// `k+"s"` existed only because the vocabulary named the same thing two ways.
	for _, k := range append(append(handlerKinds, proxyHandlerKinds...), streamServerKinds...) {
		if k == kind {
			return true
		}
	}
	return false
}

// Kinds lists every kind that declares ops — i.e. everything a resource can reach.
func Kinds() []string {
	out := []string{}
	for _, g := range V2Resources {
		name, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		if _, ok := g.Children[0].Meta["ops"].([]Verb); ok {
			out = append(out, name)
		}
	}
	return out
}

// EffectOf reports a verb's effect class for one kind.
func EffectOf(kind, verb string) (string, bool) {
	for _, v := range OpsOf(kind) {
		if v.Name == verb {
			return v.Effect, true
		}
	}
	return "", false
}

// accessGroups is what every resource declares about what it reaches: one group per
// target kind, keyed by the target's NAME, carrying where it lives and what may be
// done to it.
//
//	# functions/invoice.yaml — a plain resource file, id at the root
//	id: Qm…
//	source: "."
//	execution: { call: invoice, timeout: 10s, memory: 16MB }
//	caches:
//	    sessions:
//	        ops: [get]
//	databases:
//	    ledger:
//	        namespace: /billing     # omitted = this resource's scope, then its parents
//	        ops: [exec]
//
// The target is named ONCE with its ops beside it, instead of once per verb, and it
// carries a `namespace` — the only way to name a resource in another namespace, since
// the ref resolver understands the current scope, the root and parents and nothing
// else.
//
// These are groups nested in the resource's own document (engine.Nested), so the
// resource stays a resource: it keeps its scalar parsing, its wire keys and its
// name->id index, and nothing needs a `config:` wrapper.
//
// `ops` members are checked against that kind's verb set at load, so an op the kind
// does not implement is a compile error naming the op and the file.
func accessGroups() []*Node {
	kinds := Kinds()
	out := make([]*Node, 0, len(kinds))
	for _, kind := range kinds {
		verbs := opNames(kind)
		out = append(out, DefineGroup(kind,
			DefineIter(append([]*Attribute{
				String("namespace", IsNamespacePath(), InSection("access"),
					Doc("Namespace", "Where it lives. \"/billing\" is absolute, from the project root; \"eu\" is relative to this resource's own namespace (or the root, for a resource at the root). Omitted means this resource's own scope, then its parents.")),
				StringSlice("ops", Required(), AreVerbsOf(kind), InSection("access"),
					Doc("Ops", fmt.Sprintf("What this resource asks to do to it: %s. The target decides what it actually gets — see that resource's authorizer.", strings.Join(verbs, ", ")))),
				// Per-use modifiers. These were unexpressible while access was a
				// verb-keyed list of names: there was no per-target place to put
				// them. An entry is that place.
				Duration("timeout", InSection("access"),
					Doc("Timeout", "How long one operation on it may take before it is given up on.")),
			}, append(deliverAttributes(kind), mountAttributes(kind)...)...),
				// No Resource(...): an access entry is data under its parent, not a
				// kind. That descriptor would emit a generated type for it and would
				// mark the group as something a scope walk cares about.
				GroupDoc(fmt.Sprintf("A %s this resource reaches, and what it may do to it.", kind)),
			)))
	}
	return out
}

// EachInSet is InSet for a StringSlice.
//
// engine.InSet is scalar-only: its validator takes a T, so handing it a []string
// fails at LOAD with "invalid type passed to validator" and a line number in the
// user's yaml — not at schema-build time, where the mistake actually is. The schema
// compiles, every fixture breaks, and the error names the wrong file.
//
// Same pair AreVerbsOf already forms: check every member, and record the permitted
// set as the enum a generator, an editor and the JSON Schema all read back.
func EachInSet(vals ...string) engine.Option {
	return func(a *Attribute) {
		Validator(func(in []string) error {
			for _, v := range in {
				ok := false
				for _, want := range vals {
					if v == want {
						ok = true
						break
					}
				}
				if !ok {
					return fmt.Errorf("%q is not one of %s", v, strings.Join(vals, ", "))
				}
			}
			return nil
		})(a)
		engine.Annotate("enum", vals)(a)
	}
}

// AreVerbsOf checks every op against the kind's declared verb set at load AND records
// the set, so the same table that enforces it is the one an editor completes from and
// the JSON Schema publishes. One source of truth: the closure is derived from the
// data, not written beside it.
func AreVerbsOf(kind string) engine.Option {
	return func(a *Attribute) {
		verbsOfChecker(kind)(a)
		engine.Annotate("enum", opNames(kind))(a)
	}
}

func verbsOfChecker(kind string) engine.Option {
	return Validator(func(ops []string) error {
		for _, op := range ops {
			if _, ok := EffectOf(kind, op); !ok {
				return fmt.Errorf("%q is not something a %s does; it has %s",
					op, kind, strings.Join(opNames(kind), ", "))
			}
		}
		return nil
	})
}

var secAccess = Section("access", "Access", "What this resource may reach, and what it therefore depends on.")

// opNames is a kind's verbs, in declaration order.
func opNames(kind string) []string {
	ops := OpsOf(kind)
	out := make([]string, 0, len(ops))
	for _, v := range ops {
		out = append(out, v.Name)
	}
	return out
}

// IsNamespacePath checks the shape of a namespace path. The two forms are the ones
// the name resolver already uses for references: a leading "/" is absolute from the
// project root, anything else is relative to the resource's own namespace — which is
// the root itself for a resource at the root. Segments are namespace names, so each
// must be one.
// IsURL checks the one thing a base URL has to be: an https (or http) address with a
// host and no path stuffed into it. Not a full URL grammar — what this catches is the
// mistakes that are unambiguous, the way IsDatabaseHost did.
func IsURL() engine.Option {
	return Validator(func(u string) error {
		if u == "" {
			return nil
		}
		parsed, err := url.Parse(u)
		if err != nil {
			return fmt.Errorf("%q is not a URL: %w", u, err)
		}
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return fmt.Errorf("%q needs an https:// (or http://) scheme", u)
		}
		if parsed.Host == "" {
			return fmt.Errorf("%q names no host", u)
		}
		return nil
	})
}

func IsNamespacePath() engine.Option {
	return Validator(func(p string) error {
		if p == "" || p == "/" {
			return nil // omitted, or the project root itself
		}
		if strings.HasSuffix(p, "/") {
			return fmt.Errorf("namespace path %q must not end in /", p)
		}
		for _, seg := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
			if seg == "" {
				return fmt.Errorf("namespace path %q has an empty segment", p)
			}
			if seg == ".." {
				return fmt.Errorf("namespace path %q climbs with ..; name the namespace instead, or use a leading / for an absolute path", p)
			}
			for i, r := range seg {
				ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
				if !ok {
					return fmt.Errorf("namespace path %q: %q is not a namespace name", p, seg)
				}
			}
		}
		return nil
	})
}

// authorizerAttributes are declared on a kind that can be REACHED. The policy lives
// with the target, not with the caller: a function asks a cache for get and put, and
// the cache's authorizer is what decides that this caller, from this namespace, gets
// get only.
//
//	# caches/orders.yaml
//	authorizer:
//	    name: tier_check
//	    namespace: /shop      # same path rules as anywhere else
//
// It is a FUNCTION built against the authorizer host interface, handed the caller and
// the operation and RETURNING THE OPS that caller may perform. The accessor's `ops`
// list is a request, and a declaration of intent that keeps the config readable as
// blast radius; the answer is the target's.
//
// A resource with no authorizer is decided by its declared access alone.
func authorizerAttributes() []*Attribute {
	return []*Attribute{
		String("authorizer/name", Path("authorizer", "name"), IsVariableName(), NamesResource("function"), InSection("authorizer"),
			Doc("Authorizer", "A function that decides what a caller may do to this resource, and returns the ops it gets.")),
		String("authorizer/namespace", Path("authorizer", "namespace"), IsNamespacePath(), InSection("authorizer"),
			Doc("Authorizer Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
	}
}

var secAuthorizer = Section("authorizer", "Authorizer", "Who decides what a caller may do to this.")

// deliverAttributes answer a question the vocabulary was not asking: with a queue or
// a channel, WHAT STARTS THE WORK.
//
//	queues:
//	    orders:
//	        ops: [receive]
//	        deliver:
//	            port: http     # one of this workload's own named ports
//	            path: /work
//
// A WASM consumer needs none of it. It is dispatched — the call is the delivery, the
// host ABI is the channel, and there is no port anywhere in the picture.
//
// A CONTAINER has no host ABI. It is already running, and an item has to be handed to
// it over something, so the platform posts each one to a port the workload declared
// and a path it chose. That is Cloud Run's push-subscription shape, and it is the one
// that fits what this vocabulary already says: `limits.concurrency` is how many items
// an instance may hold at once and `limits.max-instances` is how far the platform may
// scale to keep up, which are only meaningful if the platform is the one pushing. A
// consumer that pulled would own its own concurrency and those two ceilings would
// describe nothing.
//
// It is per-USE, not per-workload, because one container can consume two queues on two
// paths, and the entry is where that is already written.
//
// Left optional rather than required-for-containers: this block hangs off the shared
// access group, which every kind gets, so "required when the consumer is a container"
// is not a condition the schema can state. A container that declares `receive` and no
// `deliver` receives nothing — the same publish-time reachability check that catches a
// port with no feeder catches this, and both are still unwritten.
func deliverAttributes(kind string) []*Attribute {
	if !isDelivered(kind) {
		return nil
	}
	return []*Attribute{
		String("deliver/port", Path("deliver", "port"), IsVariableName(), InSection("access"),
			Doc("Deliver To Port", "Which of this workload's own named ports each item is posted to. Containers only: a WASM consumer is dispatched and has no port.")),
		String("deliver/path", Path("deliver", "path"), InSection("access"),
			Doc("Deliver To Path", "The path each item is posted to, e.g. \"/work\".")),
	}
}

// mountAttributes say WHERE a filesystem is attached.
//
//	volume:
//	    pgdata:
//	        ops: [mount-rw]
//	        path: /var/lib/postgresql/data
//
// Required, because a mount with no path is not a mount — and the kind that needs this
// most is a database running in a container, where the path IS the configuration: point
// postgres at the wrong directory and it initialises an empty one on local disk and
// loses everything on the next placement.
//
// Per USE, not per volume: two workloads can mount the same volume at different paths,
// and the same workload can mount two volumes.
func mountAttributes(kind string) []*Attribute {
	if !isMounted(kind) {
		return nil
	}
	return []*Attribute{
		String("path", Required(), InSection("access"),
			Doc("Mount Path", "Absolute path inside the workload where this filesystem appears, e.g. \"/var/lib/postgresql/data\".")),
	}
}

// HandsToPrefixes is every block in this vocabulary where a resource names what it hands
// traffic on to, read off the kinds themselves.
//
// It replaces a literal list of three prefixes that both the audit and the explorer
// carried. That list was a bug waiting: `stream` was added with a `serve` block and drew
// no arrow and failed no check until it was noticed by eye, because two separate copies
// of "the ways in" had not been edited.
func HandsToPrefixes() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		if p := engine.HandsToOf(g.Children[0]); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// NamedResourceFields is every attribute that holds the NAME of another resource, with
// the kind it names and the namespace field beside it. Read off the schema, so a policy
// field added tomorrow is drawn and checked without editing a consumer.
func NamedResourceFields() []NamedField {
	out := []NamedField{}
	seen := map[string]bool{}
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		collectNamedFields(g.Children[0], &out, seen)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// NamedField is one attribute that names another resource.
type NamedField struct {
	Field     string // the attribute holding the name
	Namespace string // the attribute holding its namespace, when there is one
	Kind      string // what kind it names
}

func collectNamedFields(iter *engine.Node, out *[]NamedField, seen map[string]bool) {
	for _, a := range iter.Attributes {
		kind := engine.NamesResourceOf(a)
		if kind == "" || seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		f := NamedField{Field: a.Name, Kind: kind}
		// The namespace beside it, by the vocabulary's own convention: the same prefix
		// (`authorizer/name` -> `authorizer/namespace`), or the block's own `namespace`
		// where the name field has no prefix of its own (a workflow step's `run` sits
		// beside `namespace`).
		for _, cand := range []string{strings.TrimSuffix(a.Name, "name") + "namespace", "namespace"} {
			if f.Namespace != "" {
				break
			}
			for _, b := range iter.Attributes {
				if b.Name == cand && b.Name != a.Name {
					f.Namespace = cand
				}
			}
		}
		*out = append(*out, f)
	}
	for _, child := range iter.Children {
		if len(child.Children) > 0 {
			collectNamedFields(child.Children[0], out, seen)
		}
	}
}
