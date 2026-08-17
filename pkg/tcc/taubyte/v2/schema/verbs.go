package schema

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// Ops declares what may be done TO this kind, on the kind itself. It is the only
// place a verb is written: the access block a resource authors, the enum an editor
// completes from, the JSON Schema, and the effect class a query groups by are all
// read back off this declaration, so none of them can drift from it.
//
//	DefineGroup("caches", resource(…,
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
}

// Read/Write/Admin name a verb and its effect class in one term, so a verb cannot be
// declared without one.
func ReadOp(name string) Verb  { return Verb{name, Read} }
func WriteOp(name string) Verb { return Verb{name, Write} }
func AdminOp(name string) Verb { return Verb{name, Admin} }

// httpOps: for an ingress-shaped kind the HTTP methods ARE the verbs. GET/HEAD/
// OPTIONS classify read, the rest write.
func httpOps() []Verb {
	return []Verb{
		ReadOp("GET"), ReadOp("HEAD"), ReadOp("OPTIONS"),
		WriteOp("POST"), WriteOp("PUT"), WriteOp("PATCH"), WriteOp("DELETE"),
	}
}

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
			DefineIter([]*Attribute{
				String("namespace", IsNamespacePath(), InSection("access"),
					Doc("Namespace", "Where it lives. \"/billing\" is absolute, from the project root; \"eu\" is relative to this resource's own namespace (or the root, for a resource at the root). Omitted means this resource's own scope, then its parents.")),
				StringSlice("ops", Required(), AreVerbsOf(kind), InSection("access"),
					Doc("Ops", fmt.Sprintf("What may be done to it: %s.", strings.Join(verbs, ", ")))),
				// Per-use modifiers. These were unexpressible while access was a
				// verb-keyed list of names: there was no per-target place to put
				// them. An entry is that place.
				// Addressed the way every target is — {namespace, name} — because one
				// addressing form is the point. An authorizer is a FUNCTION, built
				// against the authorizer host interface rather than the workload one;
				// it is not a kind of its own, since it would carry exactly a
				// function's fields and nothing else.
				String("via", Path("via", "name"), IsVariableName(), InSection("access"),
					Doc("Via", "A function consulted per operation, built against the authorizer interface. It can only narrow this entry, never widen it, and a failure is a denial.")),
				String("via-namespace", Path("via", "namespace"), IsNamespacePath(), InSection("access"),
					Doc("Via Namespace", "Where that function lives. Same forms as any namespace path; omitted means this resource's own scope, then its parents.")),
				Duration("timeout", InSection("access"),
					Doc("Timeout", "How long one operation on it may take before it is given up on.")),
			},
				// No Resource(...): an access entry is data under its parent, not a
				// kind. That descriptor would emit a generated type for it and would
				// mark the group as something a scope walk cares about.
				GroupDoc(fmt.Sprintf("A %s this resource reaches, and what it may do to it.", kind)),
			)))
	}
	return out
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
