package interp

import (
	"fmt"
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
	acts  bool
	set   map[string]string
	links []link
}

type link struct {
	kind, name, verb, scope string
	// where the edge STARTS, when that is not the derived resource itself
	fromKind, fromName string
	// what KIND of edge it is, in the vocabulary's own terms. Opaque here: the
	// interpreter does not know what "read" means, it only carries the word.
	effect string
	ref    bool
}

type (
	// LoweringOption configures one lowering.
	LoweringOption func(*Lowering)
	// LinkOption configures one link out of a lowered resource.
	LinkOption func(*link)
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
// The verb is for a reader: "invokes", "gives up to".
func LinksTo(kind, name, verb string, opts ...LinkOption) LoweringOption {
	lk := link{kind: kind, name: name, verb: verb}
	for _, o := range opts {
		o(&lk)
	}
	return func(l *Lowering) { l.links = append(l.links, lk) }
}

// From says what the edge starts AT, when the derived resource is not the thing doing
// it. A queue does not hand work to another queue — the function that consumed the
// first one is what sends to the second — and an edge drawn queue-to-queue says
// something that cannot happen. Both ends are templates, like everything else here.
func From(kind, name string) LinkOption {
	return func(l *link) { l.fromKind, l.fromName = kind, name }
}

// Effect says what kind of edge this is, in the vocabulary's own words — the same
// classes its verbs use. Opaque to the interpreter, which carries it rather than
// interpreting it.
func Effect(class string) LinkOption {
	return func(l *link) { l.effect = class }
}

// AsReference marks a link that POINTS rather than acts: a queue naming its dead-letter
// queue is the same statement as the authored `dead-letter` field, and drawing the two
// differently says the lowering invented a relationship the config did not have.
func AsReference() LinkOption {
	return func(l *link) { l.ref = true }
}

// InScope says where the link's target lives, as a template. Omitted means the same
// scope as the resource that was lowered. It exists because a step may name a function
// in another namespace, and a checker that assumed otherwise would report a working
// config as broken.
func InScope(tmpl string) LinkOption {
	return func(l *link) { l.scope = tmpl }
}

// Lowers declares that a kind lowers into others. Declared on the kind's iterator,
// beside Ops() and Resource(), because it is a statement about the kind.
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
	Links  []Resolved     // what it connects to, with both ends filled in

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

	// Doc is the yaml this resource would be. See document().
	Doc string

	// Acts marks the part that does what the source declares. See Acts().
	Acts bool
}

// Source points at the authored config a derived resource came from.
type Source struct {
	Kind  string // the authored resource's kind
	Name  string // its name
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
type Resolved struct {
	Kind  string
	Name  string
	Verb  string
	Scope string // "" means the lowered resource's own scope
	// From is what the edge starts at, when that is not the resource carrying it.
	// Empty Kind means it starts at the derived resource itself.
	From struct{ Kind, Name string }
	// Effect is the vocabulary's own class for this edge, and Ref marks one that
	// points rather than acts.
	Effect string
	Ref    bool
}

func (d Derived) String() string { return d.Kind + "/" + d.Name + " (" + d.Why + ")" }

// Doc is the document this derived resource would BE — what an author would have had to
// write to get it by hand — and it is built at PLAN time because that is where the
// schema is.
//
// It has to be authorable, not merely readable. A field's compiled key is not its
// authored path: `execution/call` is one name for one attribute and three lines of yaml
// (`execution:` / `  call:`), and writing the flat key produced a document that failed
// to compile with "unknown key 'execution/call'". Generated config that would be
// rejected if anyone authored it is worse than none, so the paths come off the schema.

// document renders a derived resource as yaml, nesting each field at the path its
// attribute declares.
func document(root *engine.Node, d Derived) string {
	var b strings.Builder
	b.WriteString("# " + d.Kind + "/" + d.Name + " — generated, not in your repository\n")
	b.WriteString("# from " + d.Source.String() + "\n")
	if d.Why != "" {
		b.WriteString("# " + d.Why + "\n")
	}
	b.WriteString("#\n")
	if d.ID != "" {
		b.WriteString("id: " + d.ID + "\n")
	}

	tree := map[string]any{}
	iter := iterOfKind(root, d.Kind)
	for name, v := range d.Fields {
		place(tree, authoredPath(iter, name), fmt.Sprint(v))
	}
	writeTree(&b, tree, 0)

	for _, l := range d.Links {
		if l.Ref {
			continue // already a field above
		}
		b.WriteString("\n# " + l.Verb + " " + l.Kind + "/" + l.Name)
	}
	if len(d.Links) > 0 {
		b.WriteString("\n")
	}
	return b.String()
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

func place(tree map[string]any, path []string, value string) {
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

func writeTree(b *strings.Builder, tree map[string]any, depth int) {
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pad := strings.Repeat("    ", depth)
	for _, k := range keys {
		switch v := tree[k].(type) {
		case map[string]any:
			b.WriteString(pad + k + ":\n")
			writeTree(b, v, depth+1)
		default:
			b.WriteString(pad + k + ": " + fmt.Sprint(v) + "\n")
		}
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
		src := Source{Kind: kind, Name: name, Scope: at}
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

	for i := range out {
		out[i].Doc = document(root, out[i])
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Of != out[j].Of {
			return out[i].Of < out[j].Of
		}
		return out[i].Kind+out[i].Name < out[j].Kind+out[j].Name
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
			Links: links(l.links, res, nil, nil), Source: src, Via: via, Acts: l.acts,
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
		part, _ := el["name"].(string)
		if part == "" {
			part = at.Path // no name of its own: its position is the only handle
		}
		out = append(out, Derived{
			Kind: l.into, Name: expand(l.name, res, el, next), Of: of, Scope: scope,
			ID:  derivedID(srcID, l.into, part),
			Why: expand(l.why, res, el, next), Fields: setFor(root, l.into, l.set, res, el, next),
			Links: links(l.links, res, el, next), Source: at, Via: via, Acts: l.acts,
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
		res["name"] = p.Name
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
func links(ls []link, res, each, next map[string]any) []Resolved {
	out := make([]Resolved, 0, len(ls))
	for _, l := range ls {
		name := expand(l.name, res, each, next)
		if name == "" || strings.ContainsAny(name, "{}") {
			continue
		}
		// An unfillable SCOPE is not a scope called "{each.namespace}" — it is the
		// absent one, which means "wherever the lowered resource lives". Leaving the
		// template in was silent and total: every consumer looked for a namespace by
		// that literal name, found nothing, and drew no edge at all.
		scope := expand(l.scope, res, each, next)
		if strings.ContainsAny(scope, "{}") {
			scope = ""
		}
		r := Resolved{Kind: l.kind, Name: name, Verb: l.verb, Scope: scope, Effect: l.effect, Ref: l.ref}
		if l.fromKind != "" {
			from := expand(l.fromName, res, each, next)
			if from == "" || strings.ContainsAny(from, "{}") {
				continue // an origin that cannot be named is an edge that cannot be drawn
			}
			r.From.Kind, r.From.Name = l.fromKind, from
		}
		out = append(out, r)
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
			name, _ := res["name"].(string)
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
		}
		return false
	})
	// The same kind appears once per namespace depth, so a single mistake would be
	// reported eight times. One problem, said once.
	sort.Strings(problems)
	return slices.Compact(problems)
}
