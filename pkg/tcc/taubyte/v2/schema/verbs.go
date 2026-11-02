package schema

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
)

// Effect classes are fixed at three (read, write, admin); verbs are per kind,
// because a standard set breaks on `invoke` and collapsing publish/subscribe into
// `write` erases a security boundary.
const (
	Read  = "read"
	Write = "write"
	Admin = "admin"
)

type Verb struct {
	Name   string
	Effect string
	// Delivered marks a verb where the platform hands the work over (`receive`,
	// `subscribe`). A wasm consumer is dispatched; a container has to be posted to,
	// which is what puts a `deliver` block on these entries.
	Delivered bool

	// Inbound reverses the edge this verb draws, toward the declarer, as
	// RefSpec.Inbound does for a reference. Separate from Delivered: a secret's value
	// travels to the workload but nothing is posted to a port.
	Inbound bool

	// Mounted marks a verb that attaches a filesystem, which needs a path.
	Mounted bool
}

// Mounts marks a verb as attaching a filesystem, which gives its access entry a path.
func Mounts(v Verb) Verb { v.Mounted = true; return v }

// Arrives marks a verb whose edge points AT the declarer: the value travels from the
// target to the workload that asked for it. See Verb.Inbound — and Ref's Inbound(),
// which states the same fact about a reference.
func Arrives(v Verb) Verb { v.Inbound = true; return v }

// Read/Write name a verb and its effect class in one term, so a verb cannot be
// declared without one.
func ReadOp(name string) Verb  { return Verb{Name: name, Effect: Read} }
func WriteOp(name string) Verb { return Verb{Name: name, Effect: Write} }

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

// Ops annotates a kind with its verb set. HTTP methods are not ops: they belong to
// a gateway route, and as ops they would be a second, unrouted door.
func Ops(verbs ...Verb) NodeOption { return GroupAnnotate("ops", Verbs(verbs)) }

// Verbs is a kind's verb set, a named type so interp can check a lowering's ops
// without knowing what a Verb is.
type Verbs []Verb

// Names is the interface interp checks lowerings against. See interp.CheckLowerings.
func (vs Verbs) Names() []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Name)
	}
	return out
}

// VerbsOfIter reads a kind's declared verbs off its iterator.
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

// ServedByRoute reports whether traffic can be handed to this kind: by a gateway
// route, an L4 proxy or a channel. A kind with neither this nor ops is not a
// target.
func ServedByRoute(kind string) bool {
	// No pluralisation to undo any more: a handler kind IS the kind's name. That
	// `k+"s"` existed only because the vocabulary named the same thing two ways.
	for _, k := range append(append(handlerKinds, proxyHandlerKinds...), channelServerKinds...) {
		if k == kind {
			return true
		}
	}
	return false
}

// Kinds lists every kind that declares ops — i.e. everything a resource can reach.
func Kinds() []string { return slices.Clone(kinds()) }

var kinds = sync.OnceValue(func() []string {
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
})

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
// target kind, keyed by the target's name, with where it lives and its ops.
//
//	caches:
//	    sessions:
//	        ops: [get]
//	databases:
//	    ledger:
//	        namespace: /billing     # omitted = this resource's scope, then its parents
//	        ops: [exec]
//
// Nested in the resource's own document, so it stays a resource. `ops` are
// checked against the kind's verbs at load.
func accessGroups() []*Node {
	kinds := Kinds()
	out := make([]*Node, 0, len(kinds))
	for _, kind := range kinds {
		verbs := opNames(kind)
		out = append(out, DefineGroup(kind,
			DefineIter(append([]*Attribute{
				// Filed under a label, normally the target's name. `name` is only for reaching two
				// same-named resources in different namespaces; a lowering never sets it.
				String(accessName, IsVariableName(), InSection("access"),
					Doc("Name", "The target's real name, when it differs from the label above — only needed to reach two resources that share a name in different namespaces. Omitted means the label above is the name.")),
				String(accessScope, IsNamespacePath(), InSection("access"),
					Doc("Namespace", "Where it lives. \"/billing\" is absolute, from the project root; \"eu\" is relative to this resource's own namespace (or the root, for a resource at the root). Omitted means this resource's own scope, then its parents.")),
				StringSlice(accessOps, Required(), AreVerbsOf(kind), InSection("access"),
					Doc("Ops", fmt.Sprintf("What this resource asks to do to it: %s. The target decides what it actually gets — see that resource's authorizer.", strings.Join(verbs, ", ")))),
				// Per-use modifiers live on the entry.
				Duration(accessTimeout, InSection("access"),
					Doc("Timeout", "How long one operation on it may take before it is given up on.")),
			}, append(deliverAttributes(kind), append(mountAttributes(kind), toolsAttributes(kind)...)...)...),
				// No Resource(...): an access entry is data under its parent, not a kind.
				GroupDoc(fmt.Sprintf("%s this resource reaches, and what it may do to it.", article(kind))),
			)))
	}
	return out
}

// article prefixes a kind with a capitalized indefinite article.
func article(kind string) string {
	if kind != "" && strings.ContainsRune("aeiouAEIOU", rune(kind[0])) {
		return "An " + kind
	}
	return "A " + kind
}

// Reaches is a lowering writing an access entry, expanded into interp.Writes using
// this vocabulary's entry shape, so interp never learns what a verb is.
//
//	Reaches("receive", From("function", Each("run"), Each("namespace")))
//	Reaches("send", From("function", Each("run")),
//	    To("queue", Concat(Attr("name"), Text("_"), Next("name"))))
//
// The op must be one the target's kind declares; CheckVerbs says so at build time.
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
		// The target is the one To names, or else what the lowering produced, whose kind
		// and name the interpreter prefixes since they do not exist until the plan runs.
		at := func(field string) interp.WriteOption {
			if r.toKind == "" {
				return interp.AtProduced(interp.Text(field))
			}
			return interp.At(interp.Text(r.toKind), r.toName, interp.Text(field))
		}
		interp.Writes(interp.Text(op), append(on, at(accessOps))...)(l)

		// The namespace is To's scope when given, else the produced scope; when neither
		// fills, a bare name resolves outward to the author's resource. A namespace the
		// author wrote is never replaced.
		where := interp.Scope()
		if r.toKind != "" && !r.toScope.Empty() {
			where = r.toScope
		}
		interp.Writes(where, append(on, at(accessScope))...)(l)
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

// EachInSet is InSet for a StringSlice: engine.InSet's validator takes a scalar and
// would fail at load against the user's yaml. It also records the enum.
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

// IsURL checks an http(s) address with a host and no path; only unambiguous
// mistakes, not a full URL grammar.
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

// authorizerAttributes are on a kind that can be reached: the target owns the
// policy. The authorizer is a function handed the caller and operation that
// returns the ops granted; the accessor's `ops` is a request. Without one, the
// declared access alone decides.
//
//	authorizer:
//	    name: tier_check
//	    namespace: /shop
func authorizerAttributes() []*Attribute {
	return []*Attribute{
		String(Key("authorizer", "name"), IsVariableName(), Ref("function", ByName()), InSection("authorizer"),
			Doc("Authorizer", "A function that decides what a caller may do to this resource, and returns the ops it gets.")),
		String(Key("authorizer", "namespace"), IsNamespacePath(), InSection("authorizer"),
			Doc("Authorizer Namespace", "Where that function lives. Omitted means this resource's own scope, then its parents.")),
	}
}

var secAuthorizer = Section("authorizer", "Authorizer", "Who decides what a caller may do to this.")

// deliverAttributes say what starts the work for a container consuming a queue or
// stream: the platform posts each item to one of its named ports at a path.
//
//	queues:
//	    orders:
//	        ops: [receive]
//	        deliver:
//	            port: http
//	            path: /work
//
// A wasm consumer is dispatched and needs none. Per use, since one container may
// consume two queues. Optional, as the shared access group cannot condition on the
// consumer's tier.
func deliverAttributes(kind string) []*Attribute {
	if !isDelivered(kind) {
		return nil
	}
	return []*Attribute{
		String(Key(accessDeliver, accessPort), IsVariableName(), InSection("access"),
			Doc("Deliver To Port", "Which of this workload's own named ports each item is posted to. Containers only: a WASM consumer is dispatched and has no port.")),
		String(Key(accessDeliver, accessPath), InSection("access"),
			Doc("Deliver To Path", "The path each item is posted to, e.g. \"/work\".")),
	}
}

// mountAttributes say where a filesystem is attached, per use.
//
//	volume:
//	    pgdata:
//	        ops: [mount-rw]
//	        path: /var/lib/postgresql/data
//
// Required: a database pointed at the wrong directory initialises an empty one.
func mountAttributes(kind string) []*Attribute {
	if !isMounted(kind) {
		return nil
	}
	return []*Attribute{
		String(accessMountPath, Required(), InSection("access"),
			Doc("Mount Path", "Absolute path inside the workload where this filesystem appears, e.g. \"/var/lib/postgresql/data\".")),
	}
}

// toolsAttributes bounds an mcp entry to some of the catalogue: `call` otherwise
// means any tool the server advertises, now or later. It narrows like `ops` and
// fails closed (an unknown name grants nothing). Names only, never a pattern.
// `list` answers with the intersection.
func toolsAttributes(kind string) []*Attribute {
	if kind != "mcp" {
		return nil
	}
	return []*Attribute{
		StringSlice(accessTools, EachIsName(), InSection("access"),
			Doc("Tools", "Which of the server's tools this entry is for, by name. Omitted means every tool it advertises, including ones added later.")),
	}
}

// EachIsName is IsVariableName for a list, checked per member since a scalar
// validator handed a []string fails at load.
func EachIsName() engine.Option {
	return Validator(func(in []string) error {
		for _, v := range in {
			if !nameRegex.MatchString(v) {
				return fmt.Errorf("%q is not a name: a bound lists tools one by one, and never a pattern", v)
			}
		}
		return nil
	})
}

var nameRegex = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// References is every place this vocabulary points at another resource: fields
// resolving to an id, fields keeping a name, and blocks whose target kind the
// author picks. One list, expressed as Ref(...) with ByName, KindFrom and
// Inbound.
func References() []Reference {
	out := []Reference{}
	seen := map[string]bool{}
	for _, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		kind, _ := g.Match.(string)
		collectReferences(kind, g.Children[0], "", "", &out, seen)
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
	ByName    bool // the value stays a name rather than resolving to an id
	Inbound   bool // the relationship runs toward the declarer
	// In is the nested group whose ENTRIES carry this, empty on the resource itself. A
	// transformer entry's field is called `name`, and so is every resource's own — a
	// consumer that applied one to the other drew every function pointing at itself.
	In string
}

// collectReferences gathers iter's references. at is the key a block sits under, so a
// field in it is addressed from what holds the block.
func collectReferences(kind string, iter *engine.Node, in, at string, out *[]Reference, seen map[string]bool) {
	for _, a := range iter.Attributes {
		spec, ok := a.Meta["ref"].(engine.RefSpec)
		if !ok {
			continue
		}
		name := at + a.Name
		key := kind + "\x00" + in + "\x00" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		r := Reference{
			On: kind, Field: name, Kind: spec.Group, KindFrom: spec.KindFrom,
			ByName: spec.ByName, Inbound: spec.Inbound,
			Serves: spec.Serves, In: in,
		}
		// The namespace is the sibling called `namespace` under the same parent
		// (`source/component` -> `source/namespace`); missing it, resolution walks
		// outward and can find a different resource.
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
					r.Namespace = at + cand
				}
			}
		}
		*out = append(*out, r)
	}
	for _, child := range iter.Children {
		key, _ := child.Match.(string)
		switch {
		case key == "":
		case engine.IsBlock(child):
			// A block's fields are the holder's, one key down, and its groups are
			// entries at the block's path.
			collectReferences(kind, child, in, at+key+"/", out, seen)
		case len(child.Children) > 0:
			collectReferences(kind, child.Children[0], at+key, "", out, seen)
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

// AccessShape is how an access entry is written, read off the schema so a
// consumer recognises an access block without knowing the field names.
type AccessShape struct {
	Ops       string
	Namespace string
	Name      string

	// Timeout bounds one operation on the target, and is on the ENTRY because
	// it is per use.
	Timeout string

	// MountPath, DeliverPort and DeliverPath are per use, like Timeout.
	MountPath   string
	DeliverPort string
	DeliverPath string

	// Tools narrows an entry on a catalogue to some of what it offers — see
	// toolsAttributes. Only an mcp entry carries it.
	Tools string
}

// AccessEntryShape reports it, by looking at the access group the vocabulary attaches to
// every reachable kind: the attribute carrying that kind's verb enum is the ops field.
func AccessEntryShape() AccessShape { return accessEntryShape() }

var accessEntryShape = sync.OnceValue(func() AccessShape {
	return AccessShape{
		Ops:       accessOps,
		Namespace: accessScope,
		Name:      accessName,
		Timeout:   accessTimeout,

		// THE NESTED ONES JOINED, because a compiled key is its Key(...) joined
		// by "/" and a consumer reading a grant addresses it that way.
		MountPath:   accessMountPath,
		DeliverPort: engine.FieldName(Key(accessDeliver, accessPort)),
		DeliverPath: engine.FieldName(Key(accessDeliver, accessPath)),
		Tools:       accessTools,
	}
})

// The access entry's field names, stated once as constants: deriving them by
// walking the schema would be a package-level cycle while it is built.
const (
	accessOps   = "ops"
	accessScope = "namespace"

	// The per-use fields; pkg/project reads a grant back through them.
	accessTimeout   = "timeout"
	accessMountPath = "path"
	accessDeliver   = "deliver"
	accessPort      = "port"
	accessTools     = "tools"
	accessPath      = "path"

	// accessName overrides an entry's label as the target's identity, for reaching
	// two same-named resources in different namespaces from one accessor.
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
	// Scope takes the kind as well, since scopes come in more than one kind
	// (`namespace/shop`, `workflow/checkout`).
	Scope string // "{kind}/{name}"
	// Leaf is the document a scope keeps its own fields in, without the extension:
	// `namespace/shop/meta.yaml`.
	Leaf string
	// Content is a resource's own location, where its authored content sits when
	// `source` does not say: `agent/support.yaml` is described by `agent/support/`.
	// Separate from Scope, which is a container of resources.
	Content string // "{kind}/{name}"
}

func LayoutOf() Layout {
	return Layout{
		Resource: "{kind}/{name}.yaml",
		Scope:    "{kind}/{name}",
		Content:  "{kind}/{name}",
		Leaf:     engine.ContainerDoc(GenerationRoot(), scopeContainer),
	}
}

// NotNamed refuses one reserved word as a value: a step called `failed` would
// collide with the terminal queue `{workflow}_failed`, and the lowering's error
// would read far from the line.
func NotNamed(reserved string) engine.Option {
	return Validator(func(s string) error {
		if s == reserved {
			return fmt.Errorf("%q is reserved here: it is the name of the queue a run that gave up lands in", reserved)
		}
		return nil
	})
}
