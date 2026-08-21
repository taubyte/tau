package schema

import (
	"sort"
	"strconv"
	"strings"
)

// EVERY RELATIONSHIP BETWEEN TWO RESOURCES, resolved.
//
// A name in this vocabulary is resolved against a scope: "/billing/x" is absolute, "eu/x"
// is relative to the resource's own namespace, and a bare "x" is looked for here and then
// in each parent. That rule was written three times — the interpreter's reference
// resolver, this package's audit, and the explorer's graph — and the third copy is the
// one that made it obvious: a consumer implementing name resolution is a consumer that
// will disagree with the compiler about what a name means.
//
// So it is answered once, here, for everything that points at something: access entries,
// references that keep a name, and the blocks where a resource hands traffic on. What a
// consumer receives is a pair of ids and what the edge means.
//
// Direction is applied HERE too, because it is part of the relationship rather than a
// rendering choice: a delivered verb runs toward the consumer (work arrives), and an
// inbound reference runs toward the resource that declared it.

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
	// Handoff: traffic goes there — a route's handler, a proxy's forward, a stream's
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
	root := obj
	if inner, ok := obj["object"].(map[string]any); ok {
		root = inner
	}
	all := collect(root, "")

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

	var out []Relation
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

// accessRelations: what a resource asked to do to another, one per verb.
//
// A DELIVERED verb is turned around here. `receive` on a queue and `subscribe` on a
// channel are declared by the consumer, and the work travels the other way — drawn from
// the declaration, a queue worker has two arrows out and none in, which reads as a
// workload nothing ever starts.
func accessRelations(res compiled, find func(kind, name, own, ns string) (compiled, bool)) []Relation {
	var out []Relation
	for kind, val := range res.fields {
		entries, ok := val.(map[string]any)
		if !ok || len(OpsOf(kind)) == 0 {
			continue
		}
		for target, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				continue
			}
			to, found := find(kind, target, res.scope, field(entry, AccessEntryShape().Namespace))
			if !found {
				continue // an unresolvable target is the audit's business, not this one's
			}
			for _, op := range ops(entry) {
				effect, _ := EffectOf(kind, op)
				r := Relation{From: res.id, To: to.id, Kind: kind, Verb: op, Effect: effect, Class: Use}
				if delivered(kind, op) {
					r.From, r.To = to.id, res.id
				}
				out = append(out, r)
			}
		}
	}
	return out
}

func delivered(kind, op string) bool {
	for _, v := range OpsOf(kind) {
		if v.Name == op && v.Delivered {
			return true
		}
	}
	return false
}

// referenceRelations: every declared reference, resolved. A reference that resolves to
// an id is looked up by id; one that keeps a name is looked up the way a name is.
func referenceRelations(res compiled, byID map[string]compiled, find func(kind, name, own, ns string) (compiled, bool)) []Relation {
	var out []Relation
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
		// the entries of a nested group carry references of their own, and a list's
		// entries are ordered, so the verb says which one
		for i, el := range res.elements(ref.In) {
			r, ok := resolveReference(res, el, ref, byID, find)
			if !ok {
				continue
			}
			r.Verb = ref.In + " #" + strconv.Itoa(i+1)
			out = append(out, r)
		}
	}
	return out
}

func resolveReference(res compiled, from map[string]any, ref Reference, byID map[string]compiled, find func(kind, name, own, ns string) (compiled, bool)) (Relation, bool) {
	// A reference is a name it KEEPS, or the id it resolved to. Either way, one value —
	// at the field's own key, which for a block is more than one level down.
	val, _ := fieldValue(from, ref.Field).(string)
	if val == "" {
		return Relation{}, false
	}

	kind := ref.Kind
	if ref.KindFrom != "" {
		kind, _ = fieldValue(from, ref.KindFrom).(string)
	}
	if kind == "" {
		return Relation{}, false
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
			return Relation{}, false
		}
		r := Relation{From: res.id, To: to.id, Kind: kind, Verb: verb, Class: Depends}
		if ref.Inbound {
			r.From, r.To = to.id, res.id
		}
		return r, true
	}

	ns := ""
	if ref.Namespace != "" {
		ns = field(from, ref.Namespace)
	}
	to, found := find(kind, val, res.scope, ns)
	if !found {
		return Relation{}, false
	}
	// A reference whose KIND the author picked is where traffic goes; one whose kind the
	// schema fixed is a function named to decide something about this resource.
	class := Policy
	if ref.KindFrom != "" {
		class, verb = Handoff, "serves"
	}
	return Relation{From: res.id, To: to.id, Kind: kind, Verb: verb, Class: class}, true
}
