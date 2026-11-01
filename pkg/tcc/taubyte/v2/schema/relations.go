package schema

import (
	"sort"
	"strconv"
	"strings"
)

// Every relationship between two resources, resolved once here with the
// compiler's scoping rules so no consumer resolves names differently. Direction is
// applied here too: a delivered verb runs toward the consumer, an inbound
// reference toward its declarer.

// Relation is one resolved relationship.
type Relation struct {
	From string // resource id
	To   string // resource id
	Kind string // the target's kind
	Verb string // what to call it
	// Effect is the vocabulary's own class — read, write, admin — for a use. Empty for
	// a reference, which grants nothing.
	Effect string
	Class  Class
}

// Class is what kind of relationship it is, which is as far as this goes toward
// presentation: a consumer decides how each one looks.
type Class string

const (
	// Use: one resource acting on another, with a verb it asked for.
	Use Class = "use"
	// Handoff: traffic goes there — a route's handler, a proxy's forward, a channel's
	// serve.
	Handoff Class = "handoff"
	// Policy: a function this resource names to decide something about it.
	Policy Class = "policy"
	// Depends: a dependency that grants nothing — a source, a credential, a
	// dead-letter queue.
	Depends Class = "reference"
)

// Relations resolves every relationship in a compiled project.
func Relations(obj map[string]any) []Relation {
	declared := declaredRelations(collect(projectOf(obj), ""))
	out := make([]Relation, len(declared))
	for i, d := range declared {
		out[i] = d.Relation
	}
	return out
}

// declared is a relation with who WROTE it: Relation's direction is the flow's, which an
// inbound verb or reference turns around, and what a resource needs in order to resolve
// is the other question — the declarer needs its target, whichever way work moves.
type declared struct {
	Relation
	turned bool
}

// By is the resource that declared the relation, Of the one it names.
func (d declared) By() string {
	if d.turned {
		return d.To
	}
	return d.From
}

func (d declared) Of() string {
	if d.turned {
		return d.From
	}
	return d.To
}

func declaredRelations(all []compiled) []declared {

	// what exists, by kind and scope, so a name can be looked up the way the compiler
	// looks one up
	byName := map[string]compiled{}
	byID := map[string]compiled{}
	for _, res := range all {
		byName[res.kind+"\x00"+res.scope+"\x00"+res.name] = res
		byID[res.id] = res
	}
	// own is where the reference was written, ns what it said about where to look.
	find := func(kind, name, own, ns string) (compiled, bool) {
		for _, scope := range scopeChain(own, ns) {
			if r, ok := byName[kind+"\x00"+scope+"\x00"+name]; ok {
				return r, true
			}
		}
		return compiled{}, false
	}

	var out []declared
	for _, res := range all {
		out = append(out, accessRelations(res, find)...)
		out = append(out, referenceRelations(res, byID, find)...)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		if out[i].To != out[j].To {
			return out[i].To < out[j].To
		}
		return out[i].Verb < out[j].Verb
	})
	return out
}

// accessRelations: what a resource asked to do to another, one per verb. An
// inbound verb is turned around: `receive` and `subscribe` bring work to the
// declarer, and a secret's `get` delivers its value to it.
func accessRelations(res compiled, find func(kind, name, own, ns string) (compiled, bool)) []declared {
	var out []declared
	for kind, val := range res.fields {
		entries, ok := val.(map[string]any)
		if !ok || len(OpsOf(kind)) == 0 {
			continue
		}
		for label, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			name := entryName(label, entry)
			to, found := find(kind, name, res.scope, field(entry, AccessEntryShape().Namespace))
			if !found {
				continue // an unresolvable target is the audit's business, not this one's
			}
			for _, op := range ops(entry) {
				effect, _ := EffectOf(kind, op)
				r := declared{Relation: Relation{From: res.id, To: to.id, Kind: kind, Verb: op, Effect: effect, Class: Use}}
				if inbound(kind, op) {
					r.From, r.To, r.turned = to.id, res.id, true
				}
				out = append(out, r)
			}
		}
	}
	return out
}

// inbound reports whether this verb's edge points at the DECLARER. Two ways to say it,
// one question: a delivered verb is pushed by the platform and therefore arrives, and a
// verb marked Arrives travels toward the declarer without anything being pushed — a
// secret's value is fetched by the workload and still moves secret -> workload.
func inbound(kind, op string) bool {
	for _, v := range OpsOf(kind) {
		if v.Name == op {
			return v.Delivered || v.Inbound
		}
	}
	return false
}

// referenceRelations: every declared reference, resolved. A reference that resolves to
// an id is looked up by id; one that keeps a name is looked up the way a name is.
func referenceRelations(res compiled, byID map[string]compiled, find func(kind, name, own, ns string) (compiled, bool)) []declared {
	var out []declared
	for _, ref := range References() {
		if ref.On != res.kind {
			continue
		}
		if ref.In == "" {
			if r, ok := resolveReference(res, res.fields, ref, byID, find); ok {
				out = append(out, r)
			}
			continue
		}
		// Nested-group entries carry references too: a list's entries are numbered, a
		// named group's go by their key.
		for _, e := range entriesOf(res, ref.In) {
			r, ok := resolveReference(res, e.fields, ref, byID, find)
			if !ok {
				continue
			}
			r.Verb = ref.In + " " + e.label
			out = append(out, r)
		}
	}
	return out
}

func resolveReference(res compiled, from map[string]any, ref Reference, byID map[string]compiled, find func(kind, name, own, ns string) (compiled, bool)) (declared, bool) {
	// A reference is a name it KEEPS, or the id it resolved to. Either way, one value —
	// at the field's own key, which for a block is more than one level down.
	val, _ := fieldValue(from, ref.Field).(string)
	if val == "" {
		return declared{}, false
	}

	kind := ref.Kind
	if ref.KindFrom != "" {
		kind, _ = fieldValue(from, ref.KindFrom).(string)
	}
	if kind == "" {
		return declared{}, false
	}

	// The label is the field without the segment that holds the name: a reference is a
	// block now, so `model/name` is the model and `memory/embedder/name` the embedder.
	verb := strings.TrimSuffix(strings.TrimSuffix(ref.Field, "name"), "/")
	if verb == "" {
		verb = kind
	}
	verb = strings.ReplaceAll(verb, "/", " ")

	if !ref.ByName {
		// a resolved reference is `{<kind>: <id>}`, whatever the kind is
		to, ok := byID[val]
		if !ok {
			return declared{}, false
		}
		r := declared{Relation: Relation{From: res.id, To: to.id, Kind: kind, Verb: verb, Class: Depends}}
		if ref.Inbound {
			r.From, r.To, r.turned = to.id, res.id, true
		}
		return r, true
	}

	ns := ""
	if ref.Namespace != "" {
		// the sibling holding the namespace is addressed by its key too —
		// `authorizer/namespace` is two levels, not a flat entry
		ns, _ = fieldValue(from, ref.Namespace).(string)
	}
	to, found := find(kind, val, res.scope, ns)
	if !found {
		return declared{}, false
	}
	// Where traffic goes is declared on the reference, not inferred from KindFrom: a
	// router's kind is fixed and it is still a handoff.
	class := Policy
	if ref.Serves {
		class, verb = Handoff, "serves"
	}
	r := declared{Relation: Relation{From: res.id, To: to.id, Kind: kind, Verb: verb, Class: class}}
	// Direction belongs to the reference, however its value is stored.
	if ref.Inbound {
		r.From, r.To, r.turned = to.id, res.id, true
	}
	return r, true
}

// entry is one member of a nested group, with what to call it: `#2` for a position in a
// list, the key itself for a named entry.
type entry struct {
	label  string
	fields map[string]any
}

// entriesOf walks a nested group whichever shape it compiled to. A DefineList lands as an
// ordered []any and a DefineGroup as a map keyed by name; sorted, because a relation list
// that reorders between two compiles of the same project is a diff nobody made.
func entriesOf(res compiled, in string) []entry {
	switch held := fieldValue(res.fields, in).(type) {
	case []any:
		out := make([]entry, 0, len(held))
		for i, it := range held {
			if m, ok := it.(map[string]any); ok {
				out = append(out, entry{label: "#" + strconv.Itoa(i+1), fields: m})
			}
		}
		return out
	case map[string]any:
		names := make([]string, 0, len(held))
		for name := range held {
			names = append(names, name)
		}
		sort.Strings(names)
		out := make([]entry, 0, len(names))
		for _, name := range names {
			if m, ok := held[name].(map[string]any); ok {
				out = append(out, entry{label: name, fields: m})
			}
		}
		return out
	}
	return nil
}
