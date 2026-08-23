package schema

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
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
func Ops(verbs ...Verb) NodeOption { return GroupAnnotate("ops", Verbs(verbs)) }

// Verbs is a kind's verb set. It is a named type so the generic layer can ask it for the
// names without knowing what a Verb is: a lowering declares the op it asks for, and
// whether that op EXISTS is a question tcc has to be able to answer at build time
// without importing this vocabulary.
type Verbs []Verb

// Names is the interface interp checks lowerings against. See interp.CheckLowerings.
func (vs Verbs) Names() []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

// VerbsOfIter reads a kind's declared verbs off its iterator. Nothing outside this
// package reads them: what a verb is, and that a kind has any, is this vocabulary's
// idea — the compiler carries values at paths and never learns the word.
func VerbsOfIter(iter *Node) Verbs {
	v, _ := iter.Meta["ops"].(Verbs)
	return v
}

// OpsOf reads a kind's declared verbs back off the schema.
func OpsOf(kind string) []Verb {
	for _, g := range V2Resources {
		if name, _ := g.Match.(string); name != kind || len(g.Children) == 0 {
			continue
		}
		if v := VerbsOfIter(g.Children[0]); v != nil {
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
		if v := VerbsOfIter(g.Children[0]); v != nil {
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
				// Filed under a label, which is the target's own name in the common
				// case — that is what lets an entry be written `sessions: {ops:
				// [get]}` with nothing else. `name` is there for the one case that
				// needs it: this resource has to reach two DIFFERENT resources that
				// happen to share a name in different namespaces, which the label
				// alone cannot hold twice. Set it only then; a lowering never does.
				String(accessName, IsVariableName(), InSection("access"),
					Doc("Name", "The target's real name, when it differs from the label above — only needed to reach two resources that share a name in different namespaces. Omitted means the label above is the name.")),
				String(accessScope, IsNamespacePath(), InSection("access"),
					Doc("Namespace", "Where it lives. \"/billing\" is absolute, from the project root; \"eu\" is relative to this resource's own namespace (or the root, for a resource at the root). Omitted means this resource's own scope, then its parents.")),
				StringSlice(accessOps, Required(), AreVerbsOf(kind), InSection("access"),
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
				GroupDoc(fmt.Sprintf("%s this resource reaches, and what it may do to it.", article(kind))),
			)))
	}
	return out
}

// article prefixes a kind with the grammatically correct capitalized indefinite
// article — every kind here is a common noun, so this is cheaper than a table of
// exceptions and stays correct the day a vowel-initial kind is added.
func article(kind string) string {
	if kind != "" && strings.ContainsRune("aeiouAEIOU", rune(kind[0])) {
		return "An " + kind
	}
	return "A " + kind
}

// REACHING IS THIS VOCABULARY'S IDEA, so the term for it lives here.
//
// A lowering that wires what it produced to what already exists is, in tcc's terms, a
// value written into a resource at a path. What makes that value a VERB, the path an
// access entry, and the entry a statement about what one resource may do to another, is
// all this vocabulary's — and none of it belongs in a compiler that has to serve the next
// vocabulary too. So Reaches expands into interp.Writes, using the shape this vocabulary
// already declares for an entry.
//
//	Reaches("receive", From("function", Each("run"), Each("namespace")))
//	Reaches("send", From("function", Each("run")),
//	    To("queue", Concat(Attr("name"), Text("_"), Next("name"))))
//
// The op has to be one the target's kind declares; CheckVerbs says so at build time.
func Reaches(op string, opts ...ReachOption) interp.LoweringOption {
	r := reach{op: op}
	for _, o := range opts {
		o(&r)
	}
	return func(l *interp.Lowering) {
		var on []interp.WriteOption
		if r.fromKind != "" {
			on = append(on, interp.On(r.fromKind, r.fromName, r.fromScope))
		}
		// WHERE THE ENTRY POINTS. An entry names its target by kind and name, and the
		// target is either one the lowering NAMED — To(...) — or the resource the
		// lowering itself produced, which is what naming none means. The second case is
		// not a pair of values this vocabulary could state: neither the kind nor the name
		// exists until the plan has run, so the path says it starts there and the
		// interpreter puts its own two strings in front.
		at := func(field string) interp.WriteOption {
			if r.toKind == "" {
				return interp.AtProduced(interp.Text(field))
			}
			return interp.At(interp.Text(r.toKind), r.toName, interp.Text(field))
		}
		interp.Writes(interp.Text(op), append(on, at(accessOps))...)(l)

		// AND WHERE IT IS. An entry names its target and says where to look, and a
		// lowering that puts what it produces in a scope of its own is the one case
		// where the answer is not "here, then outward": the accessor may be the
		// function an author wrote outside that scope. Scope() is what the plan hands
		// over, absolute from the project root, and it is empty — so this write does
		// not happen — for a vocabulary that scopes nothing.
		//
		// A namespace the AUTHOR wrote on the entry is not replaced: a value goes into
		// a scalar only when the field is empty, which is the same rule every other
		// thing a lowering writes follows.
		interp.Writes(interp.Scope(), append(on, at(accessScope))...)(l)
	}
}

// ReachOption configures one Reaches.
type ReachOption func(*reach)

type reach struct {
	op string
	// The KINDS are plain names — a kind is one of this vocabulary's own — while what is
	// named and where it lives are terms, because both are read off the resource being
	// lowered.
	fromKind            string
	fromName, fromScope interp.Term
	toKind              string
	toName, toScope     interp.Term
}

// From is the side that DECLARES — the accessor, which is where the entry lands. A queue
// does not hand work to another queue: the function that consumed the first one is what
// sends to the second, and that function is what declares both.
func From(kind string, name interp.Term, scope ...interp.Term) ReachOption {
	return func(r *reach) {
		r.fromKind, r.fromName = kind, name
		if len(scope) > 0 {
			r.fromScope = scope[0]
		}
	}
}

// To is the side being reached. Omitted means the resource the lowering produced, which
// is the usual shape: something new, reached by something that already exists. That
// absence is the whole statement — there is no term for "what I produced", because
// neither its kind nor its name is a value this vocabulary could write down.
func To(kind string, name interp.Term, scope ...interp.Term) ReachOption {
	return func(r *reach) {
		r.toKind, r.toName = kind, name
		if len(scope) > 0 {
			r.toScope = scope[0]
		}
	}
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
	return func(a *engine.Attribute) {
		Validator(func(u string) error {
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
		})(a)
		// "uri" is the standard registered JSON Schema format for exactly this
		// shape — every string this validator accepts also satisfies it.
		engine.Annotate("format", "uri")(a)
	}
}

// namespacePathPattern mirrors namespacePathChecker's own grammar exactly: empty,
// the bare project root "/", or an optional leading "/" then "/"-joined segments,
// each a name starting with a letter or underscore (never a digit) and never
// trailing in "/".
const namespacePathPattern = `^(|/|/?[a-zA-Z_][a-zA-Z0-9_]*(/[a-zA-Z_][a-zA-Z0-9_]*)*)$`

func IsNamespacePath() engine.Option {
	return func(a *engine.Attribute) {
		engine.Annotate("namespacePath", true)(a)
		engine.Annotate("pattern", namespacePathPattern)(a)
		namespacePathChecker()(a)
	}
}

func namespacePathChecker() engine.Option {
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
		String(Key("authorizer", "name"), IsVariableName(), Ref("function", ByName()), InSection("authorizer"),
			Doc("Authorizer", "A function that decides what a caller may do to this resource, and returns the ops it gets.")),
		String(Key("authorizer", "namespace"), IsNamespacePath(), InSection("authorizer"),
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
		String(Key("deliver", "port"), IsVariableName(), InSection("access"),
			Doc("Deliver To Port", "Which of this workload's own named ports each item is posted to. Containers only: a WASM consumer is dispatched and has no port.")),
		String(Key("deliver", "path"), InSection("access"),
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

// References is every place this vocabulary points at another resource: fields that
// resolve to an id, fields that keep a name, and blocks whose target kind the author
// picks. ONE list, because it is one fact — "this points at a resource" — and a consumer
// asking two questions to learn it is one that will answer a new case in one place and
// forget the other.
//
// That is exactly what happened. An inbound block and a name-carrying field were two
// separate terms beside Ref, so three ways to say one thing, and the graph knew about
// them one at a time. They are Ref(...) with modifiers now: ByName where the value stays
// a name, KindFrom where the kind is chosen in the document, Inbound where the
// relationship runs the other way.
func References() []Reference {
	out := []Reference{}
	seen := map[string]bool{}
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		kind, _ := g.Match.(string)
		collectReferences(kind, g.Children[0], "", &out, seen)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].On != out[j].On {
			return out[i].On < out[j].On
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// Reference is one attribute that points at another resource.
type Reference struct {
	On       string // the kind declaring it
	Field    string // the attribute holding the target
	Kind     string // the kind it points at, when the schema fixes it
	KindFrom string // the sibling holding the kind, when the author picks it
	// Serves: traffic goes there. Declared, not inferred from KindFrom — a router is a
	// handoff whose kind is fixed.
	Serves bool
	// Namespace is the sibling holding the target's namespace, when there is one.
	Namespace string
	Prefix    string // the value's prefix, for a reference that resolves
	ByName    bool   // the value stays a name rather than resolving to an id
	Inbound   bool   // the relationship runs toward the declarer
	// In is the nested group whose ENTRIES carry this, empty on the resource itself. A
	// transformer entry's field is called `name`, and so is every resource's own — a
	// consumer that applied one to the other drew every function pointing at itself.
	In string
}

func collectReferences(kind string, iter *engine.Node, in string, out *[]Reference, seen map[string]bool) {
	for _, a := range iter.Attributes {
		spec, ok := a.Meta["ref"].(engine.RefSpec)
		if !ok {
			continue
		}
		key := kind + "\x00" + in + "\x00" + a.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		r := Reference{
			On: kind, Field: a.Name, Kind: spec.Group, KindFrom: spec.KindFrom,
			Prefix: spec.Prefix, ByName: spec.ByName, Inbound: spec.Inbound,
			Serves: spec.Serves, In: in,
		}
		// THE NAMESPACE BESIDE IT: the sibling called `namespace` under the same
		// parent. `authorizer/name` -> `authorizer/namespace`, and `source/module` ->
		// `source/namespace` — which the old rule missed, because it trimmed a "name"
		// suffix the field does not have and looked for `source/modulenamespace`. A
		// reference whose namespace is not found resolves outward from the declarer's
		// own scope and quietly finds a different resource.
		parent := ""
		if cut := strings.LastIndex(a.Name, "/"); cut >= 0 {
			parent = a.Name[:cut+1]
		}
		for _, cand := range []string{parent + "namespace", "namespace"} {
			if r.Namespace != "" {
				break
			}
			for _, b := range iter.Attributes {
				if b.Name == cand && b.Name != a.Name {
					r.Namespace = cand
				}
			}
		}
		*out = append(*out, r)
	}
	for _, child := range iter.Children {
		if key, _ := child.Match.(string); key != "" && len(child.Children) > 0 {
			collectReferences(kind, child.Children[0], key, out, seen)
		}
	}
}

// InboundBlocks is every block where a kind names what it hands traffic on to, derived
// from the references whose kind the author picks. The prefix is the field minus its
// `/name`, because that is what the block IS — nothing declares it twice.
func InboundBlocks() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range References() {
		if !r.Serves {
			continue
		}
		// A field's identity is its key, joined — `route/handler/name` — so the block it
		// sits in is everything before the last segment. It used to be a "-name" suffix,
		// back when a key spelled its own path.
		if prefix := strings.TrimSuffix(r.Field, "/name"); prefix != r.Field && !seen[prefix] {
			seen[prefix] = true
			out = append(out, prefix)
		}
	}
	sort.Strings(out)
	return out
}

// AccessShape is how an access entry is written: the field holding the ops, the one
// holding the target's namespace, and the one that can override the entry's own label as
// its identity. Read off the schema rather than assumed, so a consumer recognises an
// access block without knowing that the word is "ops".
type AccessShape struct {
	Ops       string
	Namespace string
	Name      string
}

// AccessEntryShape reports it, by looking at the access group the vocabulary attaches to
// every reachable kind: the attribute carrying that kind's verb enum is the ops field.
func AccessEntryShape() AccessShape {
	return AccessShape{Ops: accessOps, Namespace: accessScope, Name: accessName}
}

// The three fields an access entry is written with, in this vocabulary's words. Said once
// and used everywhere it matters: accessGroups declares them, Reaches writes at them, and
// the audit reads them. Deriving them by walking the schema was a way of not repeating
// them, and it made the vocabulary unable to state its own shape while it was being
// built — a package-level cycle.
const (
	accessOps   = "ops"
	accessScope = "namespace"

	// accessName OVERRIDES an entry's own label as the target's identity. An access
	// group is keyed by the target's name because that made per-target fields
	// possible without a `name:` line in the common case — but the key cannot hold
	// two entries that share it, so two resources named the same thing in different
	// namespaces could never both be reached from one accessor (omissions.go,
	// "reaching two resources that share a name in different namespaces").
	//
	// Optional, and additive: every existing document, where the label already IS
	// the name, keeps meaning exactly what it means today. Only an author who needs
	// two entries pointed at the same literal name writes this — a different label
	// per entry, `name` naming the one target both entries would otherwise collide
	// on. A lowering never sets it: it produces one entry per target and the target's
	// own name is free to use as the label, the same as it always was.
	accessName = "name"
)

// entryName is an access entry's real identity: what `name` says, or the label it is
// filed under when `name` says nothing. Every consumer that resolves an access entry to
// a resource reads it this way, so "the key is the name" stays true in the common case
// without being the only way to say it.
func entryName(label string, entry map[string]any) string {
	if n := field(entry, accessName); n != "" {
		return n
	}
	return label
}

// Layout is where a resource's document lives, as templates a consumer fills in. The
// engine walks these directories; a consumer that hardcodes the same rule is a second
// copy of it.
type Layout struct {
	Resource string // "{kind}/{name}.yaml"
	// Scope takes the kind as well as the name, because there is more than one kind of
	// scope: `namespace/shop` is one an author wrote and `workflow/checkout` is one a
	// lowering produced, and a consumer filling this in says which. It named the
	// container outright until the second kind arrived.
	Scope string // "{kind}/{name}"
	// Leaf is the document a SCOPE keeps its own fields in, without the extension —
	// `namespace/shop/meta.yaml`. It said so already and answered with the PROJECT's
	// document, which nothing had noticed because nothing read it; a lowering that
	// produces a scope reads it, and wrote `namespace/checkout/project.yaml` — a
	// directory the loader sees as having no document at all.
	Leaf string
}

func LayoutOf() Layout {
	return Layout{
		Resource: "{kind}/{name}.yaml",
		Scope:    "{kind}/{name}",
		Leaf:     engine.ContainerDoc(GenerationRoot(), scopeContainer),
	}
}
