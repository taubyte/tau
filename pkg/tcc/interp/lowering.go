package interp

import (
	"maps"
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
// queue because something in the platform keeps one. A transpiled kind names none:
// it is a way of WRITING resources that already exist, and it lowers into them at
// compile. The vocabulary gains a construct; operations gains nothing to deploy.
//
// The interpreter owns the mechanism and knows nothing about what any kind means. A
// vocabulary DECLARES its lowering as data — which kind, one per resource or one per
// element of a list, what the derived resource is called — and this file carries it out.
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
	name    Term
	why     Term
	// when: a field that has to be set for this to be produced at all
	when Term
	// acts: this part does what the source resource DECLARES — the tools an agent may
	// reach are reached by its loop
	acts   bool
	set    map[string]Term
	writes []write
}

// write is one value a lowering puts into a resource, at an attribute path.
//
// It used to be a `reach`: an accessor, a target and an op — one vocabulary's model of
// "this resource may do that to that one" sitting in the layer that must not have a
// model. Everything downstream inherited it: the engine grew terms for what an access
// entry is called, the record spoke in verbs, and the decompiler knew how to take one
// back off. None of that is the interpreter's business. What IS its business is smaller
// and says the same thing: put this value in that field.
type write struct {
	value Term
	// which resource. The KIND is a plain name — a kind is one of the vocabulary's own
	// and is checked against them at build time — while the name and the scope are terms.
	// Empty kind means the one the lowering produced.
	onKind          string
	onName, onScope Term
	// where inside it: nested group keys, entry names, and finally the field. Every
	// segment is a term.
	path []Term
	// atProduced: the path starts by naming the resource THIS lowering produced — its
	// kind, then its name. Neither is a value a lowering can state, because neither
	// exists until the plan has run; saying it structurally is what replaced a pair of
	// `{into}`/`{produced}` tokens threaded through a resolver.
	atProduced bool
}

type (
	// LoweringOption configures one lowering.
	LoweringOption func(*Lowering)
	// WriteOption configures one value a lowering writes.
	WriteOption func(*write)
)

// Into declares that a resource of this kind is produced.
//
//	Into("queue",
//	    PerEach("steps"),
//	    Named(Concat(Attr("name"), Text("_"), Each("name"))),
//	    Carries(Concat(Text("work waiting for step "), Each("name"))),
//	    Sets("max-attempts", Each("attempts")),
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
func When[T string | engine.AKey](field T) LoweringOption {
	read := Attr(field)
	return func(l *Lowering) { l.when = read }
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

// Named is what the derived resource is called: `Attr(f)` is a field of the source,
// `Each(f)` one of the element, `Next(f)` one of the element after it, and `Concat` puts
// them together with the `Text` between them.
//
// A TERM rather than a closure, so a lowering can be printed — and a term rather than a
// string, so what it is made of survives being written down. `Concat(Attr("name"),
// Text("_"), Each("name"))` and `"{name}_{each.name}"` produce the same name; only the
// first one can be read backwards, because only the first one still knows which
// underscore it wrote.
func Named(t Term) LoweringOption {
	return func(l *Lowering) { l.name = t }
}

// Carries is one line saying what this derived resource holds, built from the same terms.
// It is documentation with a job: it is what a reader sees when they ask what a
// construct became.
func Carries(t Term) LoweringOption {
	return func(l *Lowering) { l.why = t }
}

// Sets gives the derived resource one of its own fields. Without these a plan is a list
// of names: nothing downstream can tell a queue that retries four times from one that
// retries once, and — the reason this is not optional — a lowering cannot NEST, because
// the next lowering down has no values to read.
//
// A term that reads exactly one field — `Each("attempts")`, not a Concat — CARRIES that
// value rather than building a string out of it, which is what lets a scalar keep its
// type on the way through and what lets the value be read back out again.
func Sets[T string | engine.AKey](field T, value Term) LoweringOption {
	name := fieldName(field)
	return func(l *Lowering) {
		if l.set == nil {
			l.set = map[string]Term{}
		}
		l.set[name] = value
	}
}

// fieldName is how a lowering names one of the produced kind's attributes: a plain key,
// or the segments of a nested one. Same rule the engine's own terms use.
func fieldName[T string | engine.AKey](field T) string { return engine.FieldName(field) }

// LinksTo is what this derived resource connects to — the wiring INSIDE the construct.
// Without it a plan is a list of names, and a consumer that wanted to show what a
// workflow actually does would have to know what a workflow is, which is the knowledge
// that must not leave the vocabulary.
//
// Writes puts a value into a resource, at the path an author would have written it at:
//
//	Writes(Text("receive"), On("function", Each("run"), Each("namespace")),
//	    At(Text("queue"), Concat(Attr("name"), Text("_"), Each("name")), Text("ops")))
//
// It says nothing about what that means. Whether those three segments are an access
// entry, a routing table or something no vocabulary has invented yet is the vocabulary's
// affair — it builds its own term out of this one, and the interpreter carries the value.
//
// Two rules, both from the schema rather than from a convention here: a value goes into a
// LIST by being appended when it is not already there, and into a scalar only when the
// field is empty. Whichever it is, an author's own value is never replaced.
func Writes(value Term, opts ...WriteOption) LoweringOption {
	w := write{value: value}
	for _, o := range opts {
		o(&w)
	}
	return func(l *Lowering) { l.writes = append(l.writes, w) }
}

// On is the resource written INTO. Omitted means the one the lowering produced, which is
// the common case; naming another is how a lowering wires something it produced to
// something that already exists.
//
// The scope is optional and says where the name resolves: absolute from the root with a
// leading slash, and otherwise relative to where the lowered resource lives.
func On(kind string, name Term, scope ...Term) WriteOption {
	return func(w *write) {
		w.onKind, w.onName = kind, name
		if len(scope) > 0 {
			w.onScope = scope[0]
		}
	}
}

// At is the path inside that resource: the keys of whatever it is nested in, and last the
// field itself. Every segment is a term, because a segment can be a fixed key or something
// read off the config — AtProduced (below) is how a write points back at what this same
// lowering made, when the path needs to start there.
func At(path ...Term) WriteOption {
	return func(w *write) { w.path = path }
}

// AtProduced is At(...) for a path that begins by NAMING the resource this lowering
// produced: its kind, then its name, then whatever these segments say.
//
//	Writes(Text("receive"), On("function", Each("run")), AtProduced(Text("ops")))
//
// It is how a lowering wires something that already exists to the thing it just made,
// and it exists because neither the kind nor the name is a value a lowering can state.
// Both are the PLAN's, held in a local by the time a write is filled in, and this option
// says "start there" so the plan can put its own two strings in front — rather than the
// lowering stating two placeholder tokens for a resolver to substitute back.
func AtProduced(path ...Term) WriteOption {
	return func(w *write) {
		w.path, w.atProduced = path, true
	}
}

// Lowers declares that a kind lowers into others. Declared on the kind's iterator,
// beside Verbs() and Resource(), because it is a statement about the kind.
//
//	Lowers(
//	    Into("queue", PerEach("steps"), Named(Concat(Attr("name"), Text("_"), Each("name"))), …),
//	    Into("entity", Named(Concat(Attr("name"), Text("_run"))), …),
//	)
func Lowers(into ...Lowering) engine.NodeOption {
	return engine.GroupAnnotate("lowers", into)
}

// Inherited marks an attribute that what a resource lowers into carries too: a produced
// resource that does not state it gets the source's value, so the compiled output says
// what the source said and no consumer has to borrow it back.
func Inherited() engine.Option { return engine.Annotate("inherited", true) }

// inherit fills in, on a produced resource, every Inherited attribute of its source
// that the produced kind declares and the lowering left unset.
func inherit(root *engine.Node, kind, into string, res, fields map[string]any) map[string]any {
	src, dst := iterOfKind(root, kind), iterOfKind(root, into)
	if src == nil || dst == nil {
		return fields
	}
	stated := maps.Clone(fields)
	for _, a := range src.Attributes {
		if on, _ := a.Meta["inherited"].(bool); !on || !declares(dst, a.Name) {
			continue
		}
		if setsBlock(dst, a, stated) {
			continue
		}
		v, ok := attrOf(res, a)
		if !ok {
			continue
		}
		if fields == nil {
			fields = map[string]any{}
		}
		fields[a.Name] = v
	}
	return fields
}

// setsBlock is whether the lowering already set any field of the block a belongs to: a
// block is inherited whole or not at all, so a lowering's policy never meets the
// source's regions.
func setsBlock(iter *engine.Node, a *engine.Attribute, fields map[string]any) bool {
	for _, b := range iter.Attributes {
		if _, set := fields[b.Name]; set && sameBlock(a, b) {
			return true
		}
	}
	return false
}

func sameBlock(a, b *engine.Attribute) bool {
	if a.Name == b.Name {
		return true
	}
	if len(a.Path) == 0 || len(b.Path) == 0 {
		return false
	}
	x, xok := a.Path[0].(string)
	y, yok := b.Path[0].(string)
	return xok && yok && x == y
}

func declares(iter *engine.Node, name string) bool {
	return slices.ContainsFunc(iter.Attributes, func(a *engine.Attribute) bool { return a.Name == name })
}

// attrOf reads an attribute off a resource: nested by its segments when compiled, flat
// by its name when the resource is itself a lowering's fields.
func attrOf(res map[string]any, a *engine.Attribute) (any, bool) {
	if v, ok := res[a.Name]; ok {
		return v, true
	}
	if len(a.Segments) == 0 {
		return nil, false
	}
	at := res
	for _, seg := range a.Segments[:len(a.Segments)-1] {
		next, ok := at[seg].(map[string]any)
		if !ok {
			return nil, false
		}
		at = next
	}
	v, ok := at[a.Segments[len(a.Segments)-1]]
	return v, ok && v != nil
}

// Lowerable declares the two shapes of one kind: before lowering it is an ordinary
// authored resource; after lowering the SAME resource is a container for what it became.
//
//	DefineGroup("workflow", Lowerable(
//	    DefineIter(attrs, Lowers(Into("queue", …))),
//	    DefineIterGroup(attrs, resourceGroups...).With(Leaf("meta")),
//	))
//
// The authored node is returned so the declaration can sit directly inside
// DefineGroup. The compiled shape is carried as schema data until c.lower builds the
// second-pass root; Authored and every authoring consumer therefore see only before.
func Lowerable(before, after *engine.Node) *engine.Node {
	if before == nil {
		return nil
	}
	if before.Meta == nil {
		before.Meta = map[string]any{}
	}
	before.Meta["lowerable"] = after
	return before
}

// loweredShapeOf reads the after shape a kind declared with Lowerable, or nil.
func loweredShapeOf(iter *engine.Node) *engine.Node {
	if iter == nil {
		return nil
	}
	n, _ := iter.Meta["lowerable"].(*engine.Node)
	return n
}

// loweredLeafOf is the document the same resource keeps its fields in after becoming a
// container. The authored form remains one flat document and has no leaf.
func loweredLeafOf(iter *engine.Node) string {
	return engine.LeafOf(loweredShapeOf(iter))
}

// at is one node reached at one lowering depth: what a rewrite produces depends on
// nothing else, which is what makes the walk below memoizable.
type nodeAt struct {
	node  *engine.Node
	depth int
}

// LoweredRoot is the schema Compile's SECOND pass reads, and the only place the after
// shape of a Lowerable kind is ever selected. Every before-node is replaced by its
// after-node, at the same key, so `workflow` stays `workflow` and gains the container
// role — the authored root, and therefore every editor, JSON Schema and completion list,
// is untouched.
//
// THE CAP IS THE NESTING CAP. An after shape holds the vocabulary's resource groups,
// which include the lowering kinds themselves, so expanding it without a bound is a
// cyclic node graph — and interp's schema walks have no visited set (see
// MaxNamespaceDepth for the same reason). A lowering may lower again up to
// MaxLoweringDepth, and the shape reaches exactly that far.
//
// IT SHARES WHAT IT DOES NOT CHANGE. Most of a vocabulary has no lowering under it at
// all, and rebuilding those subtrees anyway is not free: v2's tree copied whole took 1.2
// SECONDS, on every compile, which the explorer does on every keystroke. A subtree with
// no Lowerable node in it comes back as itself, so only the spine down to each lowering
// kind is new — the same structural-sharing rule a persistent tree uses, and the reason
// the memo is keyed by depth as well as by node.
func LoweredRoot(root *engine.Node) *engine.Node {
	memo, open := map[nodeAt]*engine.Node{}, map[nodeAt]bool{}
	var rewrite func(*engine.Node, int) *engine.Node
	rewrite = func(n *engine.Node, depth int) *engine.Node {
		if n == nil {
			return nil
		}
		key := nodeAt{n, depth}
		if done, ok := memo[key]; ok {
			return done
		}
		// A schema is a finite tree and the depth rises on every swap, so this cannot
		// re-enter at the same depth. Saying so out loud costs one map entry and turns a
		// vocabulary that got that wrong into a shape that stops expanding rather than a
		// stack that runs out.
		if open[key] {
			return n
		}
		open[key] = true
		defer delete(open, key)

		base, before := n, (*engine.Node)(nil)
		if after := loweredShapeOf(n); after != nil && depth < MaxLoweringDepth {
			before, base = n, after
			depth++
		}

		children := make([]*engine.Node, len(base.Children))
		untouched := before == nil
		for i, child := range base.Children {
			children[i] = rewrite(child, depth)
			if children[i] != child {
				untouched = false
			}
		}
		if untouched {
			return n
		}

		out := *base
		// The after shape carries the SHAPE and the kind carries everything else it is —
		// its resource descriptor, its index footprint, its verbs, its documentation. A
		// second copy of any of that would be a second place to change it.
		out.Meta = make(map[string]any, len(base.Meta))
		for _, from := range []*engine.Node{before, base} {
			if from == nil {
				continue
			}
			for k, v := range from.Meta {
				if k != "lowerable" { // the before shape's annotation, and this is past it
					out.Meta[k] = v
				}
			}
		}
		out.Children = children
		memo[key] = &out
		return &out
	}
	return rewrite(root, 0)
}

// DeclaredWrite is one value a lowering says it will put somewhere, read off the schema
// with no config in the room. It is here so a VOCABULARY can check its own lowerings —
// whether an op is one the target kind declares is a question only the vocabulary can
// answer, and CheckLowerings must not learn the word for it.
type DeclaredWrite struct {
	// From is the kind whose lowering declares this, Into the kind it produces.
	From, Into string
	// OnKind is the kind of the resource written into; empty means the produced one.
	OnKind string
	// Path is where inside it, and Value what goes there. Both as DECLARED — terms, not
	// the strings they will become, so a consumer can ask a segment whether it is a fixed
	// key or something the plan fills in instead of scanning it for punctuation.
	//
	// A path that starts at what the lowering produced arrives here with the two segments
	// the plan would put in front already in it: the kind, which is known statically and
	// is text, and the name, which is the lowering's own Named term. So a consumer sees
	// one shape and never has to know that a write can begin somewhere implicit.
	Path  []Term
	Value Term
}

// DeclaredWrites is every value every lowering in a schema writes.
func DeclaredWrites(root *engine.Node) []DeclaredWrite {
	var out []DeclaredWrite
	eachIter(root, func(iter *engine.Node) bool {
		from := ""
		for _, g := range root.Children {
			if key, _ := g.Match.(string); key != "" && len(g.Children) > 0 && g.Children[0] == iter {
				from = key
			}
		}
		for _, l := range LoweringsOf(iter) {
			for _, w := range l.writes {
				path := []Term{}
				if w.atProduced {
					path = append(path, Text(l.into), l.name)
				}
				out = append(out, DeclaredWrite{
					From: from, Into: l.into, OnKind: w.onKind,
					Path: append(path, w.path...), Value: w.value,
				})
			}
		}
		return false
	})
	return out
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

// UsesLoweringScopes reports whether any kind puts what it lowers into a scope of its
// own — i.e. whether anything here declares Lowerable.
func UsesLoweringScopes(root *engine.Node) bool {
	return eachIter(root, func(iter *engine.Node) bool { return loweredShapeOf(iter) != nil })
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
	Fields map[string]any // its own values, from Set
	Writes []Written      // the values it puts into resources, templates filled in

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

// Written is one write with its terms filled in: a value, the resource it goes into,
// and where inside that resource it goes.
type Written struct {
	Value string
	// On is the resource it is written into. Empty Kind means the derived resource.
	On End
	// Path is the way in: nested keys first, the field last.
	Path []string
}

// End is one resource, named in a scope.
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
	var branches []string
	for name, v := range d.Fields {
		if captures(iter, name) {
			branches = append(branches, name)
			continue
		}
		place(tree, authoredPath(iter, name, d.Fields), v)
	}
	// A captured branch is stated by its key being there, so it is written only when no
	// field inside it already put it there.
	for _, name := range branches {
		if path := authoredPath(iter, name, d.Fields); !has(tree, path) {
			place(tree, path, []any{})
		}
	}

	// A write whose target is this resource is part of it, at the path it names. One that
	// goes somewhere else belongs to that resource, and is placed there instead.
	for _, w := range d.Writes {
		if w.On.Kind != "" || len(w.Path) == 0 {
			continue
		}
		put(tree, w.Path, w.Value)
	}
	return tree
}

// put walks a path of keys, making the maps it needs, and puts the value at the end. A
// LIST gets the value appended when it is not already in it — which the schema decides,
// not this function: a field that already holds a list is one.
func put(tree map[string]any, path []string, value string) {
	for _, key := range path[:len(path)-1] {
		next, _ := tree[key].(map[string]any)
		if next == nil {
			next = map[string]any{}
			tree[key] = next
		}
		tree = next
	}
	last := path[len(path)-1]
	switch have := tree[last].(type) {
	case []string:
		if !slices.Contains(have, value) {
			tree[last] = append(have, value)
		}
	case nil:
		tree[last] = value
	}
}

// authoredPath is where a field is WRITTEN, which is its attribute's Path — not its
// compiled name. A matcher in the path (an Either branch) is the branch the attribute
// capturing it holds in fields; with none, the field has no single authored location,
// so it stays flat and the document says what it can.
func authoredPath(iter *engine.Node, name string, fields map[string]any) []string {
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
		for i, seg := range a.Path {
			s, ok := seg.(string)
			if !ok {
				m, matcher := seg.(engine.StringMatcher)
				if s, ok = branchOf(iter, out, i, fields); !ok || !matcher || !m.Match(s) {
					return []string{name}
				}
			}
			out = append(out, s)
		}
		return out
	}
	return []string{name}
}

// branchOf is the branch taken at position i under prefix: the value of the attribute
// that captures the matcher there.
func branchOf(iter *engine.Node, prefix []string, i int, fields map[string]any) (string, bool) {
	for _, c := range iter.Attributes {
		if !c.Captures || len(c.Path) != i+1 {
			continue
		}
		same := true
		for j, seg := range c.Path[:i] {
			if s, ok := seg.(string); !ok || s != prefix[j] {
				same = false
				break
			}
		}
		if v, ok := fields[c.Name].(string); same && ok {
			return v, true
		}
	}
	return "", false
}

func captures(iter *engine.Node, name string) bool {
	if iter == nil {
		return false
	}
	for _, a := range iter.Attributes {
		if a.Name == name {
			return a.Captures
		}
	}
	return false
}

func has(tree map[string]any, path []string) bool {
	for i, seg := range path {
		v, ok := tree[seg]
		if !ok {
			return false
		}
		if i == len(path)-1 {
			return true
		}
		if tree, ok = v.(map[string]any); !ok {
			return false
		}
	}
	return false
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
	if inner, ok := obj[ProjectKey].(map[string]any); ok {
		scope = inner
	}

	var out []Derived
	walkResources(root, scope, "", func(kind string, iter *engine.Node, id, name string, res map[string]any, at string) {
		src := Source{Kind: kind, Name: name, ID: id, Scope: at}
		out = append(out, from(root, iter, kind, name, id, res, at, src, nil)...)
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

// setFor fills in a lowering's fields and puts any SCALAR back into the form an author
// writes. A compiled value is the wire form — a Duration is nanoseconds, a Bytes is a
// count — and a lowering copies from a compiled resource into a document that is
// supposed to be authorable. Copying `size` straight through produced
// `size: 2000000000`, which is not merely ugly: it does not compile, because Bytes
// requires a unit. Generated config that would be rejected if anyone authored it is
// worse than none.
//
// The inverse is the same one decompile uses — the scalar term carries both directions
// — so there is no second table of "how do I turn nanoseconds back into 20s".
func setFor(root *engine.Node, into string, fields map[string]Term, e env) map[string]any {
	out := set(fields, e)
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
		// Format reads the WIRE value — nanoseconds, a byte count — and filling a term in
		// has already turned everything into a string. So a scalar field takes the raw
		// value straight from the source, which is only meaningful when the term reads one
		// field and states nothing else: `Attr(Key("memory","size"))` is a value being
		// carried, a Concat is a string being built. That used to be a question about a
		// template's punctuation; it is a question about the term now.
		if _, sole := fields[a.Name].Sole(); !sole {
			continue
		}
		raw, ok := fields[a.Name].value(e)
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

// from is everything one resource lowers into, placed where its kind says it goes: inside
// the source itself when it declares Lowerable, beside it otherwise.
//
// THE SCOPE IS PRODUCED ONLY WHEN SOMETHING GOES INTO IT. A lowering every part of which
// is gated on a field nobody set produces nothing, and an empty scope is a directory that
// says a construct exists where none does — visible in a graph, in a decompile and in the
// index, with nothing in it.
func from(root, iter *engine.Node, kind, name, id string, res map[string]any, at string, src Source, via []string) []Derived {
	ls := LoweringsOf(iter)
	if len(ls) == 0 {
		return nil
	}

	into := at
	if loweredShapeOf(iter) != nil && name != "" {
		into = within(at, name)
	}

	var out []Derived
	for _, l := range ls {
		out = append(out, plan(root, l, kind, name, id, res, into, src, via)...)
	}
	return out
}

func plan(root *engine.Node, l Lowering, kind, name, srcID string, res map[string]any, scope string, src Source, via []string) []Derived {
	// What a thing is CALLED is the schema's word, and a list element uses it too.
	_, nameField := engine.IdentityOf(root)
	if !l.when.Empty() {
		if v, ok := l.when.fill(env{res: res}); !ok || v == "" {
			return nil
		}
	}
	of := kind + "/" + name

	if l.perEach == "" {
		// This env carries no scope and nothing about what the lowering produced: a name
		// that read its own scope, or pointed at AtProduced, would be circular — both only
		// exist once this Derived does. A Scope() used inside a Named() here fills with
		// nothing, so the name is EMPTY and the compile refuses it by name (see lower()).
		e := env{res: res}
		named, _ := l.name.fill(e)
		return []Derived{{
			Kind: l.into, Name: named, Of: of, Scope: scope,
			// The id is hung off the lowering's own name AS DECLARED — the term, printed —
			// so a rename of the source does not re-key what it produced.
			ID:  derivedID(srcID, l.into, l.name.String()),
			Why: describe(l.why, e), Fields: inherit(root, kind, l.into, res, setFor(root, l.into, l.set, e)),
			Writes: writes(l.writes, l.into, named, scope, e), Source: src, Via: via, Acts: l.acts,
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
		e := env{res: res, each: el, next: next}
		named, _ := l.name.fill(e)
		out = append(out, Derived{
			Kind: l.into, Name: named, Of: of, Scope: scope,
			ID:  derivedID(srcID, l.into, part),
			Why: describe(l.why, e), Fields: inherit(root, kind, l.into, res, setFor(root, l.into, l.set, e)),
			Writes: writes(l.writes, l.into, named, scope, e), Source: at, Via: via, Acts: l.acts,
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

// nest lowers again the derived resources that are themselves of a lowering kind, carrying
// the trail down so the innermost one still names what the author wrote.
func nest(root *engine.Node, parents []Derived, depth int) []Derived {
	if depth >= MaxLoweringDepth || len(parents) == 0 {
		return nil
	}
	_, nameField := engine.IdentityOf(root)
	var out []Derived
	for _, p := range parents {
		iter := iterOfKind(root, p.Kind)
		if len(LoweringsOf(iter)) == 0 {
			continue
		}
		res := p.Fields
		if res == nil {
			res = map[string]any{}
		}
		res[nameField] = p.Name
		via := append(append([]string{}, p.Via...), p.Kind+"/"+p.Name)
		out = append(out, from(root, iter, p.Kind, p.Name, p.ID, res, p.Scope, p.Source, via)...)
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

// describe is what Carries() says about this derived resource: prose for a human. A
// description that cannot be filled in WHOLE is dropped rather than shown half-written —
// it is not config, nothing reads it back and no resource carries it, so an empty line
// costs a reader a sentence while a `{each.x}` in it costs them the belief that the rest
// of what they are reading is real.
func describe(why Term, e env) string {
	s, _ := why.fill(e)
	return s
}

// set fills in the derived resource's own fields. A field whose value cannot be filled is
// DROPPED, exactly as writes() drops a write it cannot address: a term that resolved to
// nothing has nothing to put there, and the printed form — `{each.attempts}` — is a
// DIAGNOSTIC. Setting a field to it wrote a string nobody authored into a resource that
// then compiles and deploys, which is worse than the field being absent.
//
// Absent is also what the rest of the machinery already reasons about: CheckLowerings
// refuses at build time a lowering that leaves a REQUIRED field of the produced kind
// unset, so a field that can go missing here is an optional one.
func set(fields map[string]Term, e env) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for k, t := range fields {
		v, ok := t.fill(e)
		if !ok {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writes fills in each declared write. One whose value or target cannot be filled is
// DROPPED rather than emitted half-written: a step with no `on_error` has no failure
// edge, and that is the common case rather than a mistake.
//
// into/produced are the kind and name of the resource this lowering makes, which a write
// needs when it points AT it: a value written into somebody else's resource that names
// this one has to name it, and only the plan knows what it ended up called. They are
// STRINGS the plan is holding, put in front of a path that says it starts there —
// AtProduced — and never terms a lowering could have stated.
func writes(ws []write, into, produced, scope string, e env) []Written {
	// ProducedScope() is WHERE WHAT THIS LOWERING PRODUCED LIVES, absolute from the
	// project root — the one fact a write into somebody else's resource cannot work out
	// for itself once Lowerable moves the produced resources into a scope of their own. An
	// entry on a function at the root that names a queue two levels in has to say so, and
	// only the plan knows where the queue ended up.
	//
	// ABSOLUTE, not relative: the resource an entry lands on may be in the source's scope
	// (a step's function, which the author wrote) or in the produced scope (an agent's
	// loop, which the same lowering made), and one spelling has to be right from both.
	//
	// Empty when nothing was scoped, which drops the write entirely — so a vocabulary
	// that declares no Lowerable writes exactly what it wrote before.
	if scope != "" {
		e.scope = "/" + scope
	}
	out := make([]Written, 0, len(ws))
	for _, w := range ws {
		// A value that cannot be filled is a write that does not happen: the last step
		// has no next, so there is no queue for it to send to. Dropped, not written
		// half-finished. It used to be said by looking for a leftover brace in the
		// filled-in string, which also dropped a value that legitimately contained one;
		// a term simply reports that a part of it had nothing behind it.
		value, ok := w.value.fill(e)
		if !ok || value == "" {
			continue
		}

		on := End{}
		if w.onKind != "" {
			name, ok := w.onName.fill(e)
			if !ok || name == "" {
				continue
			}
			// an unfillable scope is the absent one: wherever it lives
			at, _ := w.onScope.fill(e)
			on = End{Kind: w.onKind, Name: name, Scope: at}
		}

		path := make([]string, 0, len(w.path)+2)
		// A path that starts at what this lowering produced starts at two strings the
		// plan is already holding. Assigned, not substituted.
		if w.atProduced {
			if into == "" || produced == "" {
				continue
			}
			path = append(path, into, produced)
		}
		whole := true
		for _, seg := range w.path {
			filled, ok := seg.fill(e)
			if !ok || filled == "" {
				whole = false
				break
			}
			path = append(path, filled)
		}
		if !whole || len(path) == 0 {
			continue
		}

		out = append(out, Written{Value: value, On: on, Path: path})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// walkResources visits every compiled resource, carrying the kind's iterator node so a
// caller can read its annotations, and the scope it was authored in. Which groups ARE
// scopes is read off the schema (ContainerKeys), never named here.
func walkResources(root *engine.Node, scope map[string]any, at string, fn func(kind string, iter *engine.Node, id, name string, res map[string]any, at string)) {
	walkGroups(root, root.Children, scope, at, fn)
}

// walkGroups is the walk itself. The ROOT is carried through unchanged and only the group
// list descends, because what a resource's name is called and which group is the scope
// container are the ROOT's answers: this used to recurse on a synthetic
// `&Node{Children: …}`, which carries no Meta, so `IdentityOf` returned "" and every
// resource inside a namespace was walked with an empty name. Nothing noticed while the
// name was only used for provenance — templates read the resource map directly — and it
// became visible the moment the name decided WHERE what a resource lowers into goes.
func walkGroups(root *engine.Node, groups []*engine.Node, scope map[string]any, at string, fn func(kind string, iter *engine.Node, id, name string, res map[string]any, at string)) {
	containers := ContainerKeys(root)
	_, nameField := engine.IdentityOf(root)
	for _, g := range groups {
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
			if slices.Contains(containers, key) {
				child := at
				if name != "" {
					child = strings.TrimPrefix(at+"/"+name, "/")
				}
				walkGroups(root, iter.Children, res, child, fn)
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
	// A term reports the fields it reads and nothing else, so there is no list of names
	// to skip: what the plan fills in — where it put what it produced — is not a field
	// read and never turns up here. That list used to be three strings, and a fourth
	// would have had to be remembered.
	check := func(t Term) {
		for _, r := range t.Reads() {
			scope := has
			if r.Origin == OfElement || r.Origin == OfNextElement {
				scope = each
			}
			if scope[r.Field] || seen[r.String()] {
				continue
			}
			seen[r.String()] = true
			unknown = append(unknown, r.String())
		}
	}

	check(l.name)
	check(l.why)
	if !l.when.Empty() && !has[l.when.Field()] {
		unknown = append(unknown, l.when.Field())
	}
	for _, t := range l.set {
		check(t)
	}
	for _, w := range l.writes {
		check(w.value)
		check(w.onName)
		check(w.onScope)
		for _, seg := range w.path {
			check(seg)
		}
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
		// A SCOPE A LOWERING PRODUCES HAS TO BE ONE. The shape is handed over rather
		// than named so that what is synthesized is loaded, indexed and resolved
		// through by the same definition an authored scope is, and two things have to
		// hold for that.
		//
		// THE NODE IS THE ROOT'S OWN GROUP BY THAT KEY. Its key and its Leaf are read
		// off the node handed over, and the directory they describe is loaded by
		// whatever group the ROOT has under that key — so a copy, however identical,
		// is a plan that reads fine and may be describing something else.
		//
		// AND IT IS A CONTAINER. `shape.Children[0].Group` is the same structural test
		// ContainerKeys uses and the same one a consumer sees as `container` on the
		// kind, which is what makes this a check about SHAPE rather than about which
		// group it is: any kind the vocabulary declares as a scope will do, and v2
		// takes on the container role rather than borrowing another kind's.
		if shape := loweredShapeOf(iter); shape != nil {
			switch {
			case !shape.Group:
				problems = append(problems, "a lowered shape is not a group, so it cannot hold resources of its own")
			case len(shape.Children) == 0:
				problems = append(problems, "a lowered shape holds no resources of its own, so nothing could be placed in it")
			case engine.LeafOf(shape) == "":
				problems = append(problems, "a lowered shape names no leaf document for the source's own fields")
			}
			if len(LoweringsOf(iter)) == 0 {
				problems = append(problems, "a kind declares a lowering scope and lowers into nothing")
			}
		}
		for _, l := range LoweringsOf(iter) {
			switch {
			case l.into == "":
				problems = append(problems, "a lowering names no kind")
			case !kinds[l.into]:
				problems = append(problems, "lowering into "+l.into+", which is not a kind")
			}
			if l.name.Empty() {
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
			// resource it lowers, so its terms name that resource's fields — and a name
			// that is not one is not an error anywhere else: a field nothing has resolves
			// to nothing, is left showing as `{each.attemps}` in the value, and travels
			// into a derived resource looking like config.
			//
			// A term makes the STRUCTURE of a reference unrepresentable-if-wrong — there
			// is no missing brace and no misspelt `each.` prefix to make any more — and
			// this is what still catches the other half, a field name that is simply not
			// one. Both are found here, with no config in the room.
			//
			// It is also what makes the lowering reversible in the way that matters:
			// what the shell becomes is computed from the shell's own attributes, all
			// of them declared, so nothing arrives from somewhere a reader cannot see.
			for _, unknown := range unknownInputs(root, iter, l) {
				problems = append(problems, "lowering into "+l.into+" reads "+unknown+", which is not a field of what it lowers")
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
