package interp

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// LOWERING: a kind that is REWRITTEN rather than run.
//
// The word is the compiler's, not the web's. "Transpile" is source-to-source between
// two LANGUAGES; this expresses a high-level construct in terms of the lower-level
// primitives of the SAME vocabulary, which is what LLVM does to IR and rustc does from
// HIR to MIR. A workflow is not translated into another config format — it is spelled
// out in queues and an entity.
//
// Every other kind in a vocabulary names something a component holds — a queue is a
// queue because something in the substrate keeps one. A transpiled kind names none:
// it is a way of WRITING resources that already exist, and it lowers into them at
// compile. The vocabulary gains a construct; operations gains nothing to deploy.
//
// The interpreter owns the mechanism and knows nothing about what any kind means. A
// vocabulary DECLARES its lowering as data — which kind, one per resource or one per
// element of a list, what the derived resource is called — and this file expands it.
// The alternative, a hook the vocabulary fills with Go code, was the first cut: it put
// a `switch kind { case "workflow": … }` inside the schema package, which makes
// lowering something one vocabulary does rather than something tcc offers.
//
// The line: a Lowering says WHAT is produced and what it is called. It does not carry
// arithmetic, conditionals or a second document to consult, because each of those is a
// language, and the point of declaring a lowering is that a reader can see what a
// construct becomes without running it.

// Lowering is one resource — or one per element of a list — that a lowered kind
// becomes. Its fields are unexported ON PURPOSE: this DSL is written in terms, not in
// struct literals, and the only way to build one is the terms below. The same rule the
// rest of the vocabulary follows — `String("x", Required(), InSection("y"))`, never a
// literal — and the same one PromoteEnvKeyed and StringShape follow, which keep their
// specs internal behind a function.
type Lowering struct {
	into    string
	perEach string
	name    string
	why     string
	// when: a field that has to be set for this to be produced at all
	when string
	// acts: this part does what the source resource DECLARES — the tools an agent may
	// reach are reached by its loop
	acts    bool
	set     map[string]string
	reaches []reach
}

// reach is one ACCESS a lowering declares: an accessor, a target, and the op it asks
// for. Both ends default to the derived resource itself, and both are templates.
//
// It used to be a `link` carrying a prose verb ("consumed by") and a colour class, which
// is a drawing rather than a declaration — there was nothing in it a compiler could
// emit. An access entry is what an author would have written, so it is what a lowering
// says.
type reach struct {
	op string
	// each end: kind, name and the scope its name resolves in. Empty kind means the
	// derived resource itself.
	fromKind, fromName, fromScope string
	toKind, toName, toScope       string
}

type (
	// LoweringOption configures one lowering.
	LoweringOption func(*Lowering)
	// ReachOption configures one access a lowered resource declares.
	ReachOption func(*reach)
)

// Into declares that a resource of this kind is produced.
//
//	Into("queue",
//	    PerEach("steps"),
//	    Named("{name}_{each.name}"),
//	    Carries("work waiting for step {each.name}"),
//	    Sets("max-attempts", "{each.attempts}"),
//	    LinksTo("function", "{each.run}", "invokes", InScope("{each.namespace}")),
//	)
//
// The kind has to be one the vocabulary really has: lowering into something undeclared
// is a plan nothing can carry out, and CheckLowerings says so at build time.
func Into(kind string, opts ...LoweringOption) Lowering {
	l := Lowering{into: kind}
	for _, o := range opts {
		o(&l)
	}
	return l
}

// When produces this only if the named field is set on the resource being lowered. An
// optional half of a construct — an agent's memory — becomes nothing when it is not
// declared, rather than an empty resource nobody asked for.
//
// A field being set, and nothing else. Not an expression: a lowering a reader cannot
// evaluate in their head is a lowering that has to be run to be understood.
func When(field string) LoweringOption {
	return func(l *Lowering) { l.when = field }
}

// Acts marks the part that does what the SOURCE declares.
//
// An agent's access block is its tools, and it is the LOOP that calls them: opened, the
// box should read model-and-memory-and-tools around the thing doing the calling, not a
// box with arrows leaving it for reasons the picture does not give. A workflow marks
// nothing, because no single part acts for it — each step's own function does.
//
// It changes no compiled value. It says which derived resource a consumer should hang
// the source's own edges on while it is showing the inside.
func Acts() LoweringOption {
	return func(l *Lowering) { l.acts = true }
}

// PerEach produces one of these per element of a list attribute, instead of one per
// resource. A workflow's steps are the case this exists for: the retry budget belongs
// to the step, so the queue does too.
func PerEach(list string) LoweringOption {
	return func(l *Lowering) { l.perEach = list }
}

// Named is what the derived resource is called, as a template over the source's fields
// and — under PerEach — the element's. `{name}` is the source, `{each.name}` the
// element, `{each.next.name}` the element after it. A template rather than a closure,
// so a lowering can be printed.
func Named(tmpl string) LoweringOption {
	return func(l *Lowering) { l.name = tmpl }
}

// Carries is one line saying what this derived resource holds, same syntax as Named.
// It is documentation with a job: it is what a reader sees when they ask what a
// construct became.
func Carries(tmpl string) LoweringOption {
	return func(l *Lowering) { l.why = tmpl }
}

// Sets gives the derived resource one of its own fields. Without these a plan is a list
// of names: nothing downstream can tell a queue that retries four times from one that
// retries once, and — the reason this is not optional — a lowering cannot NEST, because
// the next lowering down has no values to read.
func Sets(field, tmpl string) LoweringOption {
	return func(l *Lowering) {
		if l.set == nil {
			l.set = map[string]string{}
		}
		l.set[field] = tmpl
	}
}

// LinksTo is what this derived resource connects to — the wiring INSIDE the construct.
// Without it a plan is a list of names, and a consumer that wanted to show what a
// workflow actually does would have to know what a workflow is, which is the knowledge
// that must not leave the vocabulary.
//
// Reaches declares an ACCESS: who reaches what, with which op. The op has to be one the
// target's kind declares — `receive` on a queue, `search` on a vector — because what
// this produces is the entry an author would have written, and an entry naming a verb
// the kind does not have is not config.
//
//	Reaches("receive", From("function", "{each.run}", "{each.namespace}"))
//	Reaches("send", From("function", "{each.run}"), To("queue", "{name}_{each.next.name}"))
//
// Both ends default to the derived resource itself, so the common case names one side.
func Reaches(op string, opts ...ReachOption) LoweringOption {
	r := reach{op: op}
	for _, o := range opts {
		o(&r)
	}
	return func(l *Lowering) { l.reaches = append(l.reaches, r) }
}

// From is the side that DECLARES — the accessor, which is where the entry lands. A queue
// does not hand work to another queue: the function that consumed the first one is what
// sends to the second, and that function is what declares both.
//
// The scope is optional and says where the name resolves; omitted means the scope the
// lowered resource lives in.
func From(kind, name string, scope ...string) ReachOption {
	return func(r *reach) {
		r.fromKind, r.fromName = kind, name
		if len(scope) > 0 {
			r.fromScope = scope[0]
		}
	}
}

// To is the side being reached. Omitted means the derived resource itself, which is the
// usual shape: something the lowering produced, reached by something that already exists.
func To(kind, name string, scope ...string) ReachOption {
	return func(r *reach) {
		r.toKind, r.toName = kind, name
		if len(scope) > 0 {
			r.toScope = scope[0]
		}
	}
}

// Lowers declares that a kind lowers into others. Declared on the kind's iterator,
// beside Verbs() and Resource(), because it is a statement about the kind.
//
//	Lowers(
//	    Into("queue", PerEach("steps"), Named("{name}_{each.name}"), …),
//	    Into("entity", Named("{name}_run"), …),
//	)
func Lowers(into ...Lowering) engine.NodeOption {
	return engine.GroupAnnotate("lowers", into)
}

// LoweringsOf reads a node's declared lowerings back off the schema.
func LoweringsOf(n *engine.Node) []Lowering {
	if n == nil {
		return nil
	}
	l, _ := n.Meta["lowers"].([]Lowering)
	return l
}

// UsesLowering reports whether any kind in the schema declares a lowering.
func UsesLowering(root *engine.Node) bool {
	return eachIter(root, func(iter *engine.Node) bool { return len(LoweringsOf(iter)) > 0 })
}

// Derived is one resource a lowered resource becomes.
//
// It is a PLAN, not an emission: this reports what the compiled object would gain, and
// does not add it. What is missing before it can be emitted is not code — it is
// decisions. What id does a derived resource get, and does it survive a rename of its
// source. Does a derived queue count against the project's quota. What happens when an
// author writes a resource whose name a lowering also produces (an audit check answers
// that one already). Emitting first and deciding later would put those answers in
// whatever the first implementation happened to do.
//
// ONE OF THEM IS NOT A DECISION BUT A REQUIREMENT, and it is why Source is on this
// struct rather than computed on demand: whatever is emitted has to carry where it came
// from, because tcc DECOMPILES. A compiled project goes back to yaml, and a compiled
// project holding three queues that nobody wrote must produce a workflow — not three
// queue documents and a workflow that has quietly become something else. The marker is
// what lets the decompiler drop the derived and keep the authored, and a lowering that
// emitted anonymous resources would be a one-way door.
//
// A LOWERING ADDS; IT NEVER REPLACES. The authored resource stays exactly where it was,
// and that is what makes the round trip possible: it is the shell, and the shell is the
// metadata. Its steps are still written down, so decompiling is "keep what is authored,
// drop what is marked" rather than a reconstruction that has to infer a workflow from
// the queues it happened to produce. It is also what everything else keeps pointing at
// — a cron declares `start` on the workflow, not on the first queue.
type Derived struct {
	Kind string // the kind produced
	Name string // its name

	// ID is deterministic: the same config compiles to the same id, forever, on every
	// machine. A resource a compiler wrote is stored under its id and referred to by
	// it, so minting a fresh one each compile would make every build look like a delete
	// and a create — and two clouds compiling the same project would disagree about
	// what is the same resource.
	//
	// Derived from the SOURCE's id (not its name, which can be renamed), the kind, and
	// the part — a step name where there is one, and the lowering's own name template
	// where there is not, so a workflow rename does not re-key what it produced.
	ID     string
	Of     string         // "<kind>/<name>" of the resource it was lowered from
	Scope  string         // the namespace that resource lives in
	Why    string         // one line: what this one carries
	Fields map[string]any // its own values, expanded from Set
	Grants []Access       // the access entries it implies, both ends filled in

	// Source is the AUTHORED config responsible for this, and it is the reason a
	// derived resource is not anonymous. An error found in something nobody wrote has
	// to be reported against something somebody did: not "queue/checkout_reserve is
	// wrong" — which names a resource the author cannot find in their repository — but
	// "workflow/checkout, step 0, is wrong". The plan carries the way back or the
	// diagnostics are useless.
	Source Source

	// Via is the chain of lowerings above this one, outermost first, so a nested
	// lowering can say WHICH level went wrong. Empty when this came straight from
	// something authored.
	Via []string

	// Acts marks the part that does what the source declares. See Acts().
	Acts bool
}

// Source points at the authored config a derived resource came from.
type Source struct {
	Kind string // the authored resource's kind
	Name string // its name
	// ID is its compiled id. A lowering that produced something which lowers in turn
	// still points HERE: what a reader asks is what exists because they wrote this, and
	// the intermediate is not something they wrote.
	ID    string
	Scope string // its namespace
	Path  string // where inside its document, e.g. "steps/0". Empty for the whole resource.

	// Part is the element itself, when this came from one. It is here so a consumer can
	// show the construct AS WRITTEN — a workflow's steps, wired to each other — and not
	// only what it becomes. Both views are the same computation read from opposite
	// ends, and neither should need a second implementation that knows what a step is.
	Part map[string]any
}

func (s Source) String() string {
	at := s.Kind + "/" + s.Name
	if s.Scope != "" {
		at = s.Scope + "/" + at
	}
	if s.Path != "" {
		at += " (" + s.Path + ")"
	}
	return at
}

// Trail is the whole path from the authored resource down to this one, which is what a
// nested failure has to print: the innermost thing is broken, the outermost thing is
// what the author can go and fix.
func (d Derived) Trail() string {
	trail := d.Source.String()
	for _, v := range d.Via {
		trail += " > " + v
	}
	return trail + " > " + d.Kind + "/" + d.Name
}

// Resolved is a Link with its template expanded.
// Access is one reach with both ends filled in: the entry an author would have written,
// ready to be placed on the accessor.
type Access struct {
	// Op is the verb, and it is one of the target kind's own.
	Op string
	// From is who declares it — the accessor. Empty Kind means the derived resource.
	From End
	// To is what it reaches. Empty Kind means the derived resource.
	To End
}

// End is one side of an access: a resource named in a scope.
type End struct {
	Kind, Name string
	// Scope is where the name resolves; "" means where the lowered resource lives.
	Scope string
}

// Self reports whether this end is the derived resource itself.
func (e End) Self() bool { return e.Kind == "" }

func (d Derived) String() string { return d.Kind + "/" + d.Name + " (" + d.Why + ")" }

// Doc is the document this derived resource would BE — what an author would have had to
// write to get it by hand — and it is built at PLAN time because that is where the
// schema is.
//
// It has to be authorable, not merely readable. A field's compiled key is not its
// authored path: `execution/call` is one name for one attribute and two levels of
// document (`execution:` / `  call:`), and writing the flat key produced a resource that
// failed to compile with "unknown key 'execution/call'". Generated config that would be
// rejected if anyone authored it is worse than none, so the paths come off the schema.
//
// It is a TREE, not text. Nothing here renders yaml: this goes to yaseer over an in-memory
// layer, the same way an editor hands it a document, and yaseer is what serialises. Doing
// it by hand was a second yaml writer with its own way of getting duplicate keys wrong —
// two accesses to one kind wrote `queue:` twice, and in yaml the second is the only one
// there is.

// authored is the resource as it would have been written: every field at the path its
// attribute declares, every access the resource itself grants, and its deterministic id.
func authored(root *engine.Node, d Derived) map[string]any {
	tree := map[string]any{}
	// The id goes in under whatever this vocabulary calls it: a produced resource is
	// stored under a deterministic id like any other, and the field it states that in is
	// the schema's to name.
	if idField, _ := engine.IdentityOf(root); idField != "" && d.ID != "" {
		tree[idField] = d.ID
	}

	iter := iterOfKind(root, d.Kind)
	for name, v := range d.Fields {
		place(tree, authoredPath(iter, name), v)
	}

	// An access this resource DECLARES is part of it, exactly as an author writes one.
	// The ones it does not declare belong to the resource that does, and are placed
	// there instead.
	for _, g := range d.Grants {
		if !g.From.Self() {
			continue
		}
		verbs, where := accessFields(root, d.Kind, g.To.Kind)
		if verbs == "" {
			continue // this vocabulary does not say how one is written; see AreVerbs
		}
		block, _ := tree[g.To.Kind].(map[string]any)
		if block == nil {
			block = map[string]any{}
			tree[g.To.Kind] = block
		}
		entry, _ := block[g.To.Name].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
			block[g.To.Name] = entry
		}
		if g.To.Scope != "" && where != "" {
			entry[where] = g.To.Scope
		}
		ops, _ := entry[verbs].([]string)
		if !slices.Contains(ops, g.Op) {
			entry[verbs] = append(ops, g.Op)
		}
	}
	return tree
}

// accessFields is how THIS vocabulary writes "resource of kind `on` reaches a `target`":
// the access entry sits in a nested group of the accessor's kind, keyed by the target's
// kind, and the entry's own attributes say which of them holds the verbs and which says
// where to look. Nothing here knows a field name.
func accessFields(root *engine.Node, on, target string) (verbs, namespace string) {
	iter := iterOfKind(root, on)
	if iter == nil {
		return "", ""
	}
	for _, child := range iter.Children {
		if key, _ := child.Match.(string); key != target || len(child.Children) == 0 {
			continue
		}
		return engine.AccessFields(child.Children[0])
	}
	return "", ""
}

// authoredPath is where a field is WRITTEN, which is its attribute's Path — not its
// compiled name. A path with a matcher in it (an Either branch) has no single authored
// location, so it stays flat and the document says what it can.
func authoredPath(iter *engine.Node, name string) []string {
	if iter == nil {
		return []string{name}
	}
	for _, a := range iter.Attributes {
		if a.Name != name {
			continue
		}
		if len(a.Path) == 0 {
			return []string{name}
		}
		out := make([]string, 0, len(a.Path))
		for _, seg := range a.Path {
			s, ok := seg.(string)
			if !ok {
				return []string{name}
			}
			out = append(out, s)
		}
		return out
	}
	return []string{name}
}

func place(tree map[string]any, path []string, value any) {
	for i, seg := range path {
		if i == len(path)-1 {
			tree[seg] = value
			return
		}
		child, ok := tree[seg].(map[string]any)
		if !ok {
			child = map[string]any{}
			tree[seg] = child
		}
		tree = child
	}
}

// LoweringPlan is every resource a compiled project's lowered kinds become, in a
// stable order. Pure: same config, same plan, no network, no clock.
func LoweringPlan(root *engine.Node, obj map[string]any) []Derived {
	scope := obj
	if inner, ok := obj["object"].(map[string]any); ok {
		scope = inner
	}

	var out []Derived
	walkResources(root, scope, "", func(kind string, iter *engine.Node, id, name string, res map[string]any, at string) {
		src := Source{Kind: kind, Name: name, ID: id, Scope: at}
		for _, l := range LoweringsOf(iter) {
			out = append(out, plan(root, l, kind, name, id, res, at, src, nil)...)
		}
	})

	// A lowering may lower again: what a construct becomes can itself be a construct.
	// Each round reads the fields the round above produced, which is why Set exists.
	//
	// The cap is a real guard rather than a formality — a vocabulary whose kind A
	// lowers into B and B into A is a build mistake, and without a cap it is a build
	// mistake that hangs the compiler instead of reporting itself.
	out = append(out, nest(root, out, 1)...)

	// A TOTAL order. Sorting on a pair that two derived resources can share leaves the
	// rest to map iteration, and the plan stops being a pure function of the config —
	// which is what decides collisions, grant order and every id downstream of them.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Of != b.Of {
			return a.Of < b.Of
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		// Two elements of one list can share a name — which is a fault the audit reports,
		// and until somebody fixes it the order still has to be the same every time.
		if a.Source.Path != b.Source.Path {
			return a.Source.Path < b.Source.Path
		}
		return a.ID < b.ID
	})
	return out
}

// MaxLoweringDepth is how many times a lowering may lower again. Eight, the same cap
// the namespace nesting uses, and for the same reason: the structure has to be finite
// and a cycle has to end in a message rather than a hang.
const MaxLoweringDepth = 8

// soleReference reports the raw value of a template that is exactly one reference —
// `{memory/size}` — and nothing else. A template that BUILDS a string ("{name}_failed")
// has no single value behind it, and one that names nothing has none at all.
func soleReference(tmpl string, res, each, next map[string]any) (any, bool) {
	if len(tmpl) < 3 || tmpl[0] != '{' || tmpl[len(tmpl)-1] != '}' || strings.Contains(tmpl[1:len(tmpl)-1], "{") {
		return nil, false
	}
	key := tmpl[1 : len(tmpl)-1]
	src := res
	if rest, ok := strings.CutPrefix(key, "each.next."); ok {
		src, key = next, rest
	} else if rest, ok := strings.CutPrefix(key, "each."); ok {
		src, key = each, rest
	}
	if src == nil {
		return nil, false
	}
	v, ok := src[key]
	return v, ok
}

// setFor expands a lowering's fields and puts any SCALAR back into the form an author
// writes. A compiled value is the wire form — a Duration is nanoseconds, a Bytes is a
// count — and a lowering copies from a compiled resource into a document that is
// supposed to be authorable. Copying `size` straight through produced
// `size: 2000000000`, which is not merely ugly: it does not compile, because Bytes
// requires a unit. Generated config that would be rejected if anyone authored it is
// worse than none.
//
// The inverse is the same one decompile uses — the scalar term carries both directions
// — so there is no second table of "how do I turn nanoseconds back into 20s".
func setFor(root *engine.Node, into string, fields map[string]string, res, each, next map[string]any) map[string]any {
	out := set(fields, res, each, next)
	if len(out) == 0 {
		return out
	}
	iter := iterOfKind(root, into)
	if iter == nil {
		return out
	}
	for _, a := range iter.Attributes {
		v, ok := out[a.Name]
		if !ok {
			continue
		}
		sc, isScalar := a.Meta["scalar"].(engine.ScalarSpec)
		if !isScalar {
			continue
		}
		// Format reads the WIRE value — nanoseconds, a byte count — and `expand` has
		// already turned everything into a string. So a scalar field takes the raw
		// value straight from the source, which is only meaningful when the template is
		// one reference and nothing else: "{memory/size}" is a value being carried,
		// "{name}_x" is a string being built.
		raw, ok := soleReference(fields[a.Name], res, each, next)
		if !ok {
			continue
		}
		// Format takes a selector to the object HOLDING the field, which is how the
		// decompiler calls it, so it gets a one-field object to hold.
		holder := object.New[object.Refrence]()
		one := object.New[object.Refrence]()
		one.Set(a.Name, raw)
		sel := holder.Child("x")
		if err := sel.Add(one); err != nil {
			continue
		}
		if err := sc.Format(sel, a.Name); err != nil {
			continue // leave the wire form rather than lose the value
		}
		if formatted, err := sel.GetString(a.Name); err == nil && formatted != "" {
			out[a.Name] = formatted
		}
		_ = v
	}
	return out
}

func plan(root *engine.Node, l Lowering, kind, name, srcID string, res map[string]any, scope string, src Source, via []string) []Derived {
	// What a thing is CALLED is the schema's word, and a list element uses it too.
	_, nameField := engine.IdentityOf(root)
	if l.when != "" {
		if v, ok := lookup(l.when, res, nil, nil); !ok || v == "" {
			return nil
		}
	}
	of := kind + "/" + name

	if l.perEach == "" {
		return []Derived{{
			Kind: l.into, Name: expand(l.name, res, nil, nil), Of: of, Scope: scope,
			ID:  derivedID(srcID, l.into, l.name),
			Why: expand(l.why, res, nil, nil), Fields: setFor(root, l.into, l.set, res, nil, nil),
			Grants: reaches(l.reaches, res, nil, nil), Source: src, Via: via, Acts: l.acts,
		}}
	}

	items, _ := res[l.perEach].([]any)
	out := make([]Derived, 0, len(items))
	for i, it := range items {
		el, ok := it.(map[string]any)
		if !ok {
			continue
		}
		var next map[string]any
		if i+1 < len(items) {
			next, _ = items[i+1].(map[string]any)
		}
		// The element's own position, so a diagnostic can point at the lines that
		// produced this and not merely at the document that holds them.
		at := src
		at.Path = l.perEach + "/" + strconv.Itoa(i)
		at.Part = el
		part, _ := el[nameField].(string)
		if part == "" {
			part = at.Path // no name of its own: its position is the only handle
		}
		out = append(out, Derived{
			Kind: l.into, Name: expand(l.name, res, el, next), Of: of, Scope: scope,
			ID:  derivedID(srcID, l.into, part),
			Why: expand(l.why, res, el, next), Fields: setFor(root, l.into, l.set, res, el, next),
			Grants: reaches(l.reaches, res, el, next), Source: at, Via: via, Acts: l.acts,
		})
	}
	return out
}

// derivedID hangs a derived resource's id off what produced it. Empty when the source
// has no id, because an id derived from nothing is a collision waiting for a second
// project.
func derivedID(srcID, kind, part string) string {
	if srcID == "" {
		return ""
	}
	id, err := engine.Derive(engine.DeriveResourceID, srcID, kind, part)
	if err != nil {
		return ""
	}
	return id
}

// nest expands the derived resources that are themselves of a lowering kind, carrying
// the trail down so the innermost one still names what the author wrote.
func nest(root *engine.Node, parents []Derived, depth int) []Derived {
	if depth >= MaxLoweringDepth || len(parents) == 0 {
		return nil
	}
	_, nameField := engine.IdentityOf(root)
	var out []Derived
	for _, p := range parents {
		iter := iterOfKind(root, p.Kind)
		ls := LoweringsOf(iter)
		if len(ls) == 0 {
			continue
		}
		res := p.Fields
		if res == nil {
			res = map[string]any{}
		}
		res[nameField] = p.Name
		via := append(append([]string{}, p.Via...), p.Kind+"/"+p.Name)
		for _, l := range ls {
			out = append(out, plan(root, l, p.Kind, p.Name, p.ID, res, p.Scope, p.Source, via)...)
		}
	}
	return append(out, nest(root, out, depth+1)...)
}

// iterOfKind finds a kind's iterator anywhere in the schema, including inside the scope
// container, since a lowering may produce a kind that only nests.
func iterOfKind(root *engine.Node, kind string) *engine.Node {
	var found *engine.Node
	var walk func(groups []*engine.Node)
	walk = func(groups []*engine.Node) {
		for _, g := range groups {
			if len(g.Children) == 0 {
				continue
			}
			if key, _ := g.Match.(string); key == kind && found == nil {
				found = g.Children[0]
			}
			if it := g.Children[0]; it.Group && len(it.Children) > 0 {
				walk(it.Children)
			}
		}
	}
	walk(root.Children)
	return found
}

// set expands the derived resource's own fields.
func set(fields map[string]string, res, each, next map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for k, tmpl := range fields {
		out[k] = expand(tmpl, res, each, next)
	}
	return out
}

// links expands each declared link. One whose name template cannot be filled is
// DROPPED rather than emitted half-written: a step with no `on-error` has no failure
// edge, and that is the common case rather than a mistake.
func reaches(rs []reach, res, each, next map[string]any) []Access {
	out := make([]Access, 0, len(rs))
	for _, r := range rs {
		end := func(kind, name, scope string) (End, bool) {
			if kind == "" {
				return End{}, true // the derived resource itself
			}
			n := expand(name, res, each, next)
			// A name that cannot be filled is an access that does not exist: the last
			// step has no next, so there is no queue for it to send to. Dropped, not
			// emitted half-written.
			if n == "" || strings.ContainsAny(n, "{}") {
				return End{}, false
			}
			sc := expand(scope, res, each, next)
			if strings.ContainsAny(sc, "{}") {
				sc = "" // an unfillable scope is the absent one: wherever the resource lives
			}
			return End{Kind: kind, Name: n, Scope: sc}, true
		}

		from, ok := end(r.fromKind, r.fromName, r.fromScope)
		if !ok {
			continue
		}
		to, ok := end(r.toKind, r.toName, r.toScope)
		if !ok {
			continue
		}
		out = append(out, Access{Op: r.op, From: from, To: to})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// expand fills `{field}` from the resource and `{each.field}` from the list element.
// A name that cannot be filled is left as it was written, so a broken template shows up
// as a visible `{typo}` in the plan rather than as a silently empty name.
func expand(tmpl string, res, each, next map[string]any) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(tmpl, '{')
		if open < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		close := strings.IndexByte(tmpl[open:], '}')
		if close < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		close += open
		b.WriteString(tmpl[:open])
		key := tmpl[open+1 : close]
		if v, ok := lookup(key, res, each, next); ok && v != "" {
			b.WriteString(v)
		} else {
			// left as written, so an unfilled template is visible in the plan as
			// `{typo}` rather than as a silently shortened name
			b.WriteString(tmpl[open : close+1])
		}
		tmpl = tmpl[close+1:]
	}
}

func lookup(key string, res, each, next map[string]any) (string, bool) {
	src := res
	// `{each.next.x}` is the FOLLOWING element's x. An ordered list whose elements can
	// only see themselves lowers into independent things; a chain — step, then the next
	// step — is most of the reason a list is ordered in the first place, and without
	// this the vocabulary would have to make the author write the order twice.
	//
	// It stops at one step. Arbitrary navigation is a query language, and the shapes
	// that need it (a branch, a join) need more than a template anyway.
	if rest, ok := strings.CutPrefix(key, "each.next."); ok {
		if next == nil {
			return "", false
		}
		src, key = next, rest
	} else if rest, ok := strings.CutPrefix(key, "each."); ok {
		if each == nil {
			return "", false
		}
		src, key = each, rest
	}
	v, ok := src[key]
	if !ok {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	}
	// A field that is a block or a list has no place in a name; leaving it unfilled
	// makes that visible rather than printing "map[]".
	return "", false
}

// walkResources visits every compiled resource, carrying the kind's iterator node so a
// caller can read its annotations, and the scope it was authored in. The scope
// container is read off the schema (containerKey), never named here.
func walkResources(root *engine.Node, scope map[string]any, at string, fn func(kind string, iter *engine.Node, id, name string, res map[string]any, at string)) {
	container := containerKey(root)
	_, nameField := engine.IdentityOf(root)
	for _, g := range root.Children {
		key, _ := g.Match.(string)
		if key == "" || len(g.Children) == 0 {
			continue
		}
		instances, ok := scope[key].(map[string]any)
		if !ok {
			continue
		}
		iter := g.Children[0]
		for id, v := range instances {
			res, ok := v.(map[string]any)
			if !ok {
				continue
			}
			name, _ := res[nameField].(string)
			if key == container {
				child := at
				if name != "" {
					child = strings.TrimPrefix(at+"/"+name, "/")
				}
				walkResources(&engine.Node{Children: iter.Children}, res, child, fn)
				continue
			}
			// A compiled resource is KEYED by its id and does not carry it as a field,
			// so the walk hands it down rather than the plan digging for it.
			fn(key, iter, id, name, res, at)
		}
	}
}

// unsatisfied names the required attributes of the produced kind that the lowering does
// not set and that nothing else fills in. A field with a Default or one the platform
// Generates needs no help; everything else has to come from the lowering.
func unsatisfied(root *engine.Node, l Lowering) []string {
	iter := iterOfKind(root, l.into)
	if iter == nil {
		return nil
	}
	var missing []string
	for _, a := range iter.Attributes {
		if !a.Required || a.Default != nil {
			continue
		}
		if _, gen := a.Meta["generated"]; gen {
			continue
		}
		if _, der := a.Meta["derived"]; der {
			continue
		}
		if _, ok := l.set[a.Name]; ok {
			continue
		}
		missing = append(missing, a.Name)
	}
	sort.Strings(missing)
	return missing
}

// unknownInputs names every template reference a lowering makes to a field that does
// not exist — on the resource, or on the elements of the list it iterates.
func unknownInputs(root, iter *engine.Node, l Lowering) []string {
	has := map[string]bool{}
	for _, a := range iter.Attributes {
		has[a.Name] = true
	}
	// the identity every resource carries, which the interpreter sets rather than the kind
	// declaring it — and which the schema names (engine.Identity)
	idField, nameField := engine.IdentityOf(root)
	for _, id := range []string{idField, nameField} {
		if id != "" {
			has[id] = true
		}
	}

	each := map[string]bool{}
	if l.perEach != "" {
		for _, child := range iter.Children {
			if key, _ := child.Match.(string); key == l.perEach && len(child.Children) > 0 {
				for _, a := range child.Children[0].Attributes {
					each[a.Name] = true
				}
			}
		}
	}

	var unknown []string
	seen := map[string]bool{}
	check := func(tmpl string) {
		for _, key := range templateKeys(tmpl) {
			field, scope := key, has
			if rest, ok := strings.CutPrefix(key, "each.next."); ok {
				field, scope = rest, each
			} else if rest, ok := strings.CutPrefix(key, "each."); ok {
				field, scope = rest, each
			}
			if scope[field] || seen[key] {
				continue
			}
			seen[key] = true
			unknown = append(unknown, "{"+key+"}")
		}
	}

	check(l.name)
	check(l.why)
	if l.when != "" && !has[l.when] {
		unknown = append(unknown, l.when)
	}
	for _, tmpl := range l.set {
		check(tmpl)
	}
	for _, r := range l.reaches {
		check(r.fromName)
		check(r.fromScope)
		check(r.toName)
		check(r.toScope)
	}
	sort.Strings(unknown)
	return unknown
}

// loweringCycles finds every cycle in "kind A lowers into kind B".
func loweringCycles(root *engine.Node) []string {
	into := map[string][]string{}
	eachIter(root, func(iter *engine.Node) bool {
		for _, g := range root.Children {
			key, _ := g.Match.(string)
			if key == "" || len(g.Children) == 0 || g.Children[0] != iter {
				continue
			}
			for _, l := range LoweringsOf(iter) {
				into[key] = append(into[key], l.into)
			}
		}
		return false
	})

	var found []string
	var walk func(kind string, seen []string)
	walk = func(kind string, seen []string) {
		if i := slices.Index(seen, kind); i >= 0 {
			found = append(found, strings.Join(append(seen[i:], kind), " -> "))
			return
		}
		for _, next := range into[kind] {
			walk(next, append(append([]string{}, seen...), kind))
		}
	}
	for kind := range into {
		walk(kind, nil)
	}
	sort.Strings(found)
	return slices.Compact(found)
}

// unknownVerbs is every verb a lowering asks for that the kind it is asked of does not
// have.
func unknownVerbs(root *engine.Node, l Lowering) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range l.reaches {
		kind := r.toKind
		if kind == "" {
			kind = l.into // the derived resource itself is what is reached
		}
		if seen[kind+"\x00"+r.op] {
			continue
		}
		seen[kind+"\x00"+r.op] = true

		iter := iterOfKind(root, kind)
		if iter == nil {
			continue // an unknown kind is already reported, once, above
		}
		ops := engine.VerbsOf(iter)
		if ops == nil {
			out = append(out, "\""+r.op+"\" on "+kind+", which nothing may reach")
			continue
		}
		if !slices.Contains(ops.Names(), r.op) {
			out = append(out, "\""+r.op+"\" on "+kind+", which has "+strings.Join(ops.Names(), ", "))
		}
	}
	sort.Strings(out)
	return out
}

// templateKeys is every `{…}` in a template.
func templateKeys(tmpl string) []string {
	var out []string
	for {
		open := strings.IndexByte(tmpl, '{')
		if open < 0 {
			return out
		}
		close := strings.IndexByte(tmpl[open:], '}')
		if close < 0 {
			return out
		}
		close += open
		out = append(out, tmpl[open+1:close])
		tmpl = tmpl[close+1:]
	}
}

// CheckLowerings verifies at BUILD time that every lowering names a kind the
// vocabulary has and fills a name. A lowering into a kind nobody declared is a plan
// that cannot be carried out, and finding that when someone runs the compiler is
// finding it in the wrong place.
func CheckLowerings(root *engine.Node) []string {
	kinds := map[string]bool{}
	for _, g := range root.Children {
		if key, _ := g.Match.(string); key != "" {
			kinds[key] = true
		}
	}

	var problems []string
	eachIter(root, func(iter *engine.Node) bool {
		for _, l := range LoweringsOf(iter) {
			switch {
			case l.into == "":
				problems = append(problems, "a lowering names no kind")
			case !kinds[l.into]:
				problems = append(problems, "lowering into "+l.into+", which is not a kind")
			}
			if strings.TrimSpace(l.name) == "" {
				problems = append(problems, "lowering into "+l.into+" produces no name")
			}
			// A lowering has to produce a resource that could actually EXIST. A kind's
			// required fields are required of anything of that kind, including what a
			// compiler wrote — and the mistake this catches is a real one: a workflow
			// lowering into an `entity` for its run state, when an entity is a
			// code-bearing compute instance whose source, entrypoint, timeout and
			// memory nothing was setting. That is a resource nobody could instantiate,
			// and without this check it was a plan that read fine.
			for _, missing := range unsatisfied(root, l) {
				problems = append(problems, "lowering into "+l.into+" leaves "+missing+" unset, which that kind requires")
			}
			// EVERY INPUT MUST EXIST ON THE SHELL. A lowering is a function of the
			// resource it lowers, so its templates name that resource's fields — and a
			// name that is not one is not an error anywhere else: expand leaves an
			// unfilled `{each.attemps}` in the value, which travels into a derived
			// resource and looks like config.
			//
			// It is also what makes the lowering reversible in the way that matters:
			// what the shell becomes is computed from the shell's own attributes, all
			// of them declared, so nothing arrives from somewhere a reader cannot see.
			for _, unknown := range unknownInputs(root, iter, l) {
				problems = append(problems, "lowering into "+l.into+" reads "+unknown+", which is not a field of what it lowers")
			}
			// EVERY OP MUST BE ONE THE TARGET KIND HAS. What a lowering produces is the
			// entry an author would have written, and an entry naming a verb the kind
			// does not declare is not config — it compiles nowhere and grants nothing.
			// The prose verbs this replaced ("consumed by") were exactly that, which is
			// why they could never be emitted.
			for _, bad := range unknownVerbs(root, l) {
				problems = append(problems, "lowering into "+l.into+" asks for "+bad)
			}
		}
		return false
	})
	// The record of what a compile produced sits beside the object under one key. A
	// vocabulary that declares a group by that name would have the two overwrite each
	// other, and the failure would look like resources going missing.
	for _, g := range root.Children {
		if key, _ := g.Match.(string); key == LoweredKey {
			problems = append(problems, "a group is called "+LoweredKey+", which is where a compile records what it lowered")
		}
	}

	// A CYCLE IS A BUILD MISTAKE, and it is one nobody's config can fix: a kind that
	// lowers into a kind that lowers back into it produces resources forever. The runtime
	// cap stops the hang, and stopping is all it does — the plan it returns is silently
	// truncated, which is a lie about what a config becomes. Said here instead, where a
	// vocabulary author can see it without writing any config at all.
	for _, cycle := range loweringCycles(root) {
		problems = append(problems, "lowerings cycle: "+cycle)
	}

	// The same kind appears once per namespace depth, so a single mistake would be
	// reported eight times. One problem, said once.
	sort.Strings(problems)
	return slices.Compact(problems)
}
