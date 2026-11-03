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

// Lowering: a kind rewritten rather than run, expressed in the same vocabulary's
// lower-level primitives (a workflow becomes queues and an entity). A vocabulary
// declares its lowering as data, what is produced and what it is called, and
// this file carries it out. No arithmetic, conditionals or second documents: a
// reader can see what a construct becomes without running it.

// Lowering is one resource, or one per list element, that a lowered kind
// becomes. Its fields are unexported: it is built only from the terms below.
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
	// atProduced: the path starts by naming the resource this lowering produced, its
	// kind then its name; neither exists until the plan has run.
	atProduced bool
}

type (
	// LoweringOption configures one lowering.
	LoweringOption func(*Lowering)
	// WriteOption configures one value a lowering writes.
	WriteOption func(*write)
)

// Into declares that a resource of this kind is produced:
//
//	Into("queue",
//	    PerEach("steps"),
//	    Named(Concat(Attr("name"), Text("_"), Each("name"))),
//	    Carries(Concat(Text("work waiting for step "), Each("name"))),
//	    Sets("max-attempts", Each("attempts")),
//	)
//
// CheckLowerings refuses a kind the vocabulary does not have.
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

// Acts marks the part that does what the source declares (an agent's loop, which
// calls its tools), so a consumer hangs the source's own edges on it. It changes
// no compiled value.
func Acts() LoweringOption {
	return func(l *Lowering) { l.acts = true }
}

// PerEach produces one of these per element of a list attribute, instead of one per
// resource. A workflow's steps are the case this exists for: the retry budget belongs
// to the step, so the queue does too.
func PerEach(list string) LoweringOption {
	return func(l *Lowering) { l.perEach = list }
}

// Named is what the derived resource is called: `Attr(f)` a field of the source,
// `Each(f)` one of the element, `Next(f)` one of the next element, joined by
// `Concat` with `Text`. A term, so a lowering can be printed and read backwards.
func Named(t Term) LoweringOption {
	return func(l *Lowering) { l.name = t }
}

// Carries is one line saying what this derived resource holds, built from the same terms.
// It is documentation with a job: it is what a reader sees when they ask what a
// construct became.
func Carries(t Term) LoweringOption {
	return func(l *Lowering) { l.why = t }
}

// Sets gives the derived resource one of its own fields; without them a nested
// lowering has no values to read. A term reading exactly one field carries that
// value, keeping its type, rather than building a string.
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

// Writes puts a value into a resource, at the path an author would write it at:
//
//	Writes(Text("receive"), On("function", Each("run"), Each("namespace")),
//	    At(Text("queue"), Concat(Attr("name"), Text("_"), Each("name")), Text("ops")))
//
// What those segments mean is the vocabulary's affair. A list gets the value
// appended when absent; a scalar is set only when empty. An author's own value
// is never replaced.
func Writes(value Term, opts ...WriteOption) LoweringOption {
	w := write{value: value}
	for _, o := range opts {
		o(&w)
	}
	return func(l *Lowering) { l.writes = append(l.writes, w) }
}

// On is the resource written into; omitted means the one the lowering produced.
// The optional scope says where the name resolves: absolute with a leading
// slash, otherwise relative to where the lowered resource lives.
func On(kind string, name Term, scope ...Term) WriteOption {
	return func(w *write) {
		w.onKind, w.onName = kind, name
		if len(scope) > 0 {
			w.onScope = scope[0]
		}
	}
}

// At is the path inside that resource, the field last; every segment is a term.
func At(path ...Term) WriteOption {
	return func(w *write) { w.path = path }
}

// AtProduced is At(...) for a path beginning at the resource this lowering
// produced, its kind then its name, which only the plan holds:
//
//	Writes(Text("receive"), On("function", Each("run")), AtProduced(Text("ops")))
func AtProduced(path ...Term) WriteOption {
	return func(w *write) {
		w.path, w.atProduced = path, true
	}
}

// Lowers declares that a kind lowers into others, on the kind's iterator:
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

// Lowerable declares the two shapes of one kind: authored, an ordinary resource;
// lowered, a container for what it became.
//
//	DefineGroup("workflow", Lowerable(
//	    DefineIter(attrs, Lowers(Into("queue", …))),
//	    DefineIterGroup(attrs, resourceGroups...).With(Leaf("meta")),
//	))
//
// The authored node is returned; only the compile's second pass sees the other.
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

type nodeIn struct {
	node, container *engine.Node
}

// LoweredRoot is the schema Compile's second pass reads: each Lowerable kind's
// before-node replaced by its after-node at the same key, the authored root
// untouched. Inside an after shape a held kind is expanded only if that kind's
// lowerings produce it. A subtree with no Lowerable node comes back as itself.
func LoweredRoot(root *engine.Node) *engine.Node {
	memo, open := map[nodeIn]*engine.Node{}, map[nodeIn]bool{}
	var rewrite func(n, container *engine.Node, kind string) *engine.Node
	rewrite = func(n, container *engine.Node, kind string) *engine.Node {
		if n == nil {
			return nil
		}
		key := nodeIn{n, container}
		if done, ok := memo[key]; ok {
			return done
		}
		if open[key] {
			return n
		}
		open[key] = true
		defer delete(open, key)

		base, before := n, (*engine.Node)(nil)
		if after := loweredShapeOf(n); after != nil && (container == nil || produces(container, kind)) {
			before, base, container = n, after, n
		}

		children := make([]*engine.Node, len(base.Children))
		untouched := before == nil
		childKind, _ := base.Match.(string)
		for i, child := range base.Children {
			children[i] = rewrite(child, container, childKind)
			if children[i] != child {
				untouched = false
			}
		}
		if untouched {
			return n
		}

		out := *base
		// The after shape carries the shape; the kind carries everything else it is.
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
	return rewrite(root, nil, "")
}

func produces(container *engine.Node, kind string) bool {
	return slices.ContainsFunc(LoweringsOf(container), func(l Lowering) bool { return l.into == kind })
}

// DeclaredWrite is one value a lowering will put somewhere, read off the schema,
// so a vocabulary can check its own lowerings.
type DeclaredWrite struct {
	// From is the kind whose lowering declares this, Into the kind it produces.
	From, Into string
	// OnKind is the kind of the resource written into; empty means the produced one.
	OnKind string
	// Path and Value as declared terms. A path starting at what the lowering
	// produced arrives with the produced kind and Named term already in front.
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

// Derived is one resource a lowered resource becomes: a plan, not an emission.
// It carries Source because tcc decompiles: a compiled project holding queues
// nobody wrote must come back as the workflow. A lowering adds and never
// replaces, so the authored shell stays and is what everything points at.
type Derived struct {
	Kind string // the kind produced
	Name string // its name

	// ID is deterministic, from the source's id (not its name), the kind and the
	// part, so builds do not re-key what they produced and two clouds agree.
	ID     string
	Of     string         // "<kind>/<name>" of the resource it was lowered from
	Scope  string         // the namespace that resource lives in
	Why    string         // one line: what this one carries
	Fields map[string]any // its own values, from Set
	Writes []Written      // the values it puts into resources, templates filled in

	// Source is the authored config responsible, so an error is reported against
	// what the author can find ("workflow/checkout, step 0").
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
	// ID is its compiled id. A nested lowering still points here, at what the reader
	// wrote.
	ID    string
	Scope string // its namespace
	Path  string // where inside its document, e.g. "steps/0". Empty for the whole resource.

	// Part is the element itself, so a consumer can show the construct as written.
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

// Doc is the document this derived resource would be, built at plan time from
// the schema's authored paths: a field's compiled key is not its path. It is a
// tree that yaseer serialises, not text.

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
		place(tree, authoredPath(iter, name, d.Fields), typedFor(iter, name, v))
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

// authoredPath is where a field is written: its attribute's Path, with each
// matcher the branch its capturing attribute holds; with none, the field stays
// flat.
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

// typedFor is a value as the attribute it lands on reads it. A lowering's fixed text is
// a string, and the engine reads a string as a bool for no author, so text the
// vocabulary states for a bool is turned into one here.
func typedFor(iter *engine.Node, name string, v any) any {
	s, ok := v.(string)
	if !ok || iter == nil {
		return v
	}
	for _, a := range iter.Attributes {
		if a.Name != name {
			continue
		}
		if a.Type == engine.TypeBool {
			if b, err := strconv.ParseBool(s); err == nil {
				return b
			}
		}
		return v
	}
	return v
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

	names := map[string]string{}
	walkResources(root, scope, "", func(_ string, _ *engine.Node, id, name string, _ map[string]any, _ string) {
		names[id] = name
	})

	var out []Derived
	walkResources(root, scope, "", func(kind string, iter *engine.Node, id, name string, res map[string]any, at string) {
		src := Source{Kind: kind, Name: name, ID: id, Scope: at}
		out = append(out, from(root, iter, kind, name, id, res, at, src, nil, names)...)
	})

	// A lowering may lower again: what a construct becomes can itself be a construct.
	// Each round reads the fields the round above produced, which is why Set exists.
	out = append(out, nest(root, out, names)...)

	// A total order: a pair two derived resources can share would leave the rest to
	// map iteration.
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

// setFor fills in a lowering's fields and puts any scalar back into its authored
// form: a compiled Duration is nanoseconds and `size: 2000000000` does not
// compile. The inverse is the one decompile uses.
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
		if _, ok := out[a.Name]; !ok {
			continue
		}
		// A reference read out of the compiled object is the id it resolved to; the
		// produced document says it as an author would, by name, and compiles it again.
		if _, isRef := a.Meta["ref"].(engine.RefSpec); isRef {
			if id, ok := out[a.Name].(string); ok {
				if name, known := e.names[id]; known && name != "" {
					out[a.Name] = name
				}
			}
			continue
		}
		sc, isScalar := a.Meta["scalar"].(engine.ScalarSpec)
		if !isScalar {
			continue
		}
		// A scalar takes the raw wire value from the source, meaningful only when the
		// term reads one field and states nothing else.
		if _, sole := fields[a.Name].Sole(); !sole {
			continue
		}
		raw, ok := fields[a.Name].value(e)
		if !ok {
			continue
		}
		// Format takes a selector to the object holding the field.
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
	}
	return out
}

// from is everything one resource lowers into, inside the source when it
// declares Lowerable, beside it otherwise. The scope is produced only when
// something goes into it.
func from(root, iter *engine.Node, kind, name, id string, res map[string]any, at string, src Source, via []string, names map[string]string) []Derived {
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
		out = append(out, plan(root, l, kind, name, id, res, into, src, via, names)...)
	}
	return out
}

func plan(root *engine.Node, l Lowering, kind, name, srcID string, res map[string]any, scope string, src Source, via []string, names map[string]string) []Derived {
	// What a thing is CALLED is the schema's word, and a list element uses it too.
	_, nameField := engine.IdentityOf(root)
	if !l.when.Empty() {
		if v, ok := l.when.fill(env{res: res}); !ok || v == "" {
			return nil
		}
	}
	of := kind + "/" + name

	if l.perEach == "" {
		// No scope here: a name reading its own scope would be circular, so it fills
		// empty and the compile refuses it (see lower()).
		e := env{res: res, names: names}
		named, _ := l.name.fill(e)
		return []Derived{{
			Kind: l.into, Name: named, Of: of, Scope: scope,
			// Hung off the lowering's declared name, so renaming the source does not re-key
			// what it produced.
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
		e := env{res: res, each: el, next: next, names: names}
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
// the trail down so the innermost one still names what the author wrote. New refuses a
// cyclic vocabulary, so this ends.
func nest(root *engine.Node, parents []Derived, names map[string]string) []Derived {
	if len(parents) == 0 {
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
		out = append(out, from(root, iter, p.Kind, p.Name, p.ID, res, p.Scope, p.Source, via, names)...)
	}
	return append(out, nest(root, out, names)...)
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

// describe is what Carries() says, dropped unless it can be filled in whole.
func describe(why Term, e env) string {
	s, _ := why.fill(e)
	return s
}

// set fills in the derived resource's own fields, dropping one that cannot be
// filled rather than writing its printed form. CheckLowerings already refuses a
// lowering leaving a required field unset.
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

// writes fills in each declared write, dropping one whose value or target cannot
// be filled (a step with no on_error). into and produced are what this lowering
// made, which only the plan holds.
func writes(ws []write, into, produced, scope string, e env) []Written {
	// ProducedScope() is where the produced resources live, absolute from the root,
	// so one spelling is right from the source's scope and the produced one. Empty
	// when nothing was scoped, which drops the write.
	if scope != "" {
		e.scope = "/" + scope
	}
	out := make([]Written, 0, len(ws))
	for _, w := range ws {
		// A value that cannot be filled is a write that does not happen.
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
		// A path starting at what this lowering produced starts at the plan's two
		// strings.
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

// walkResources visits every compiled resource with its kind's iterator and the
// scope it was authored in; which groups are scopes comes from ContainerKeys.
func walkResources(root *engine.Node, scope map[string]any, at string, fn func(kind string, iter *engine.Node, id, name string, res map[string]any, at string)) {
	walkGroups(root, root.Children, scope, at, fn)
}

// walkGroups carries the root through unchanged and descends only the group
// list: the name field and the scope container are the root's answers.
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
	// A term reports the fields it reads and nothing else, so there is no list of
	// names to skip.
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

// CheckLowerings verifies at build time that every lowering names a kind the
// vocabulary has and fills a name.
func CheckLowerings(root *engine.Node) []string {
	kinds := map[string]bool{}
	for _, g := range root.Children {
		if key, _ := g.Match.(string); key != "" {
			kinds[key] = true
		}
	}

	var problems []string
	eachIter(root, func(iter *engine.Node) bool {
		// A scope a lowering produces must be the root's own group by that key, and a
		// container, so what is synthesized is loaded, indexed and resolved by the same
		// definition as an authored scope.
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
			// A lowering must produce a resource that could exist: the produced kind's
			// required fields are required of it too.
			for _, missing := range unsatisfied(root, l) {
				problems = append(problems, "lowering into "+l.into+" leaves "+missing+" unset, which that kind requires")
			}
			// Every input must exist on the shell: a field name that is not one would
			// resolve to nothing and travel into a derived resource as `{each.attemps}`.
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

	for _, cycle := range loweringCycles(root) {
		problems = append(problems, "lowerings cycle: "+cycle)
	}

	// The same kind appears once per namespace depth, so a single mistake would be
	// reported eight times. One problem, said once.
	sort.Strings(problems)
	return slices.Compact(problems)
}
