package interp

import (
	"sort"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
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

// Lowering is one resource — or one per element of a list — that a transpiled kind
// becomes.
type Lowering struct {
	// Into is the kind produced. It has to be a kind the vocabulary really has:
	// lowering into something undeclared is a plan nothing can carry out, and
	// CheckLowerings says so at build time rather than at compile.
	Into string

	// PerEach names a list attribute whose elements each produce one of these. Empty
	// means one per resource. A workflow's steps are the case this exists for: the
	// retry budget belongs to the step, so the queue does too.
	PerEach string

	// Name is what the derived resource is called, as a template. Why is one line
	// saying what it carries, same syntax. See expand for the syntax; both are
	// deliberately templates rather than closures, so a lowering can be printed.
	Name string
	Why  string

	// Set is the derived resource's own fields, as templates. Without them a plan is a
	// list of names: nothing downstream can tell a queue that retries four times from
	// one that retries once, and — the reason this is not optional — a lowering cannot
	// NEST, because the next lowering down has no values to read.
	Set map[string]string

	// Links are what this derived resource connects to — the wiring INSIDE the
	// construct. Without them a plan is a list of names, and a consumer that wants to
	// show what a workflow actually does would have to know what a workflow is, which
	// is the knowledge that must not leave the vocabulary.
	//
	// With them, tcc answers "what is inside this resource" completely enough to draw:
	// nodes from the lowerings, edges from the links, both computed here.
	Links []Link
}

// Link is one edge out of a derived resource, to another derived resource or to an
// authored one. Both ends are templates over the same fields Name is.
type Link struct {
	To   string // "<kind>/<name template>"
	Verb string // what the edge means to a reader: "invokes", "gives up to"
	// Scope is where the target lives, as a template. Empty means the same scope as
	// the resource that was lowered. It exists because a step may name a function in
	// another namespace, and a checker that assumed otherwise would report a working
	// config as broken.
	Scope string
}

// Lowers declares that a kind lowers into others. Declared on the kind's iterator,
// beside Ops() and Resource(), because it is a statement about the kind.
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
// author writes a resource whose name a lowering also produces (CheckPlan answers that
// one already). Emitting first and deciding later would put those answers in whatever
// the first implementation happened to do.
type Derived struct {
	Kind   string         // the kind produced
	Name   string         // its name
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
}

// Source points at the authored config a derived resource came from.
type Source struct {
	Kind  string // the authored resource's kind
	Name  string // its name
	Scope string // its namespace
	Path  string // where inside its document, e.g. "steps/0". Empty for the whole resource.
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
}

func (d Derived) String() string { return d.Kind + "/" + d.Name + " (" + d.Why + ")" }

// LoweringPlan is every resource a compiled project's lowered kinds become, in a
// stable order. Pure: same config, same plan, no network, no clock.
func LoweringPlan(root *engine.Node, obj map[string]any) []Derived {
	scope := obj
	if inner, ok := obj["object"].(map[string]any); ok {
		scope = inner
	}

	var out []Derived
	walkResources(root, scope, "", func(kind string, iter *engine.Node, name string, res map[string]any, at string) {
		src := Source{Kind: kind, Name: name, Scope: at}
		for _, l := range LoweringsOf(iter) {
			out = append(out, plan(l, kind, name, res, at, src, nil)...)
		}
	})

	// A lowering may lower again: what a construct becomes can itself be a construct.
	// Each round reads the fields the round above produced, which is why Set exists.
	//
	// The cap is a real guard rather than a formality — a vocabulary whose kind A
	// lowers into B and B into A is a build mistake, and without a cap it is a build
	// mistake that hangs the compiler instead of reporting itself.
	out = append(out, nest(root, out, 1)...)

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

func plan(l Lowering, kind, name string, res map[string]any, scope string, src Source, via []string) []Derived {
	of := kind + "/" + name
	if l.PerEach == "" {
		return []Derived{{
			Kind: l.Into, Name: expand(l.Name, res, nil), Of: of, Scope: scope,
			Why: expand(l.Why, res, nil), Fields: set(l.Set, res, nil),
			Links: links(l.Links, res, nil), Source: src, Via: via,
		}}
	}

	items, _ := res[l.PerEach].([]any)
	out := make([]Derived, 0, len(items))
	for i, it := range items {
		el, ok := it.(map[string]any)
		if !ok {
			continue
		}
		// The element's own position, so a diagnostic can point at the lines that
		// produced this and not merely at the document that holds them.
		at := src
		at.Path = l.PerEach + "/" + strconv.Itoa(i)
		out = append(out, Derived{
			Kind: l.Into, Name: expand(l.Name, res, el), Of: of, Scope: scope,
			Why: expand(l.Why, res, el), Fields: set(l.Set, res, el),
			Links: links(l.Links, res, el), Source: at, Via: via,
		})
	}
	return out
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
			out = append(out, plan(l, p.Kind, p.Name, res, p.Scope, p.Source, via)...)
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
func set(fields map[string]string, res, each map[string]any) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for k, tmpl := range fields {
		out[k] = expand(tmpl, res, each)
	}
	return out
}

// links expands each declared link. One whose name template cannot be filled is
// DROPPED rather than emitted half-written: a step with no `on-error` has no failure
// edge, and that is the common case rather than a mistake.
func links(ls []Link, res, each map[string]any) []Resolved {
	out := make([]Resolved, 0, len(ls))
	for _, l := range ls {
		kind, tmpl, found := strings.Cut(l.To, "/")
		if !found {
			continue
		}
		name := expand(tmpl, res, each)
		if name == "" || strings.ContainsAny(name, "{}") {
			continue
		}
		out = append(out, Resolved{Kind: kind, Name: name, Verb: l.Verb, Scope: expand(l.Scope, res, each)})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// expand fills `{field}` from the resource and `{each.field}` from the list element.
// A name that cannot be filled is left as it was written, so a broken template shows up
// as a visible `{typo}` in the plan rather than as a silently empty name.
func expand(tmpl string, res, each map[string]any) string {
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
		if v, ok := lookup(key, res, each); ok && v != "" {
			b.WriteString(v)
		} else {
			// left as written, so an unfilled template is visible in the plan as
			// `{typo}` rather than as a silently shortened name
			b.WriteString(tmpl[open : close+1])
		}
		tmpl = tmpl[close+1:]
	}
}

func lookup(key string, res, each map[string]any) (string, bool) {
	src := res
	if rest, ok := strings.CutPrefix(key, "each."); ok {
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
func walkResources(root *engine.Node, scope map[string]any, at string, fn func(kind string, iter *engine.Node, name string, res map[string]any, at string)) {
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
		for _, v := range instances {
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
			fn(key, iter, name, res, at)
		}
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
			case l.Into == "":
				problems = append(problems, "a lowering names no kind")
			case !kinds[l.Into]:
				problems = append(problems, "lowering into "+l.Into+", which is not a kind")
			}
			if strings.TrimSpace(l.Name) == "" {
				problems = append(problems, "lowering into "+l.Into+" produces no name")
			}
		}
		return false
	})
	sort.Strings(problems)
	return problems
}
