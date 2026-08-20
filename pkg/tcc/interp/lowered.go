package interp

import (
	"sort"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// WHAT THE OBJECT SAYS ABOUT WHAT NOBODY WROTE.
//
// Compiling lowers, so the object holds resources the author never wrote and access
// entries they never granted. Without a record of that, the object cannot be taken apart
// again: decompile would write a queue into somebody's repository as though they had
// asked for it.
//
// So the compile records it, beside the object rather than inside the resources —
// exactly where the index subtree sits, and for the same reason. A resource must remain
// the shape the schema declares: a `lowered-from` key on one would be a key the
// vocabulary does not have, rejected by the strictness that makes an unknown key an
// error at all.
//
// Read it with LoweredOf. What it makes possible: decompile that stops at what somebody
// wrote, a console that shows what one workflow became, and — later — a shell that stores
// only what its lowering cannot give back.

// LoweredKey is where the record sits in the compiled object.
const LoweredKey = "lowered"

// Provenance is where a resource nobody wrote came from.
type Provenance struct {
	// From is the compiled id of the authored resource responsible.
	From string `json:"from"`
	// At is where inside it — "steps/0" — and empty when the whole resource is.
	At string `json:"at,omitempty"`
	// Kind and Name are its own, so a reader has them without walking the object.
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Scope is the namespace it was placed in.
	Scope string `json:"scope,omitempty"`
}

// Granted is one access entry a lowering added to a resource somebody DID write.
type Granted struct {
	// On is the compiled id of the resource that now declares it.
	On string `json:"on"`
	// Kind, Name and Op are the entry itself: what it reaches and with which verb.
	Kind string `json:"kind"`
	Name string `json:"name"`
	Op   string `json:"op"`
	// From is the compiled id of the authored resource whose lowering added it.
	From string `json:"from"`
}

// Lowered is everything one compile lowered.
type Lowered struct {
	// Resources is every resource the compile produced, by its compiled id.
	Resources map[string]Provenance
	// Entries is every access it added to a resource that already existed.
	Entries []Granted
}

// Of is what one authored resource became: the ids it produced and the entries it added.
func (l Lowered) Of(id string) ([]string, []Granted) {
	var made []string
	for derived, p := range l.Resources {
		if p.From == id {
			made = append(made, derived)
		}
	}
	sort.Strings(made)

	var added []Granted
	for _, g := range l.Entries {
		if g.From == id {
			added = append(added, g)
		}
	}
	return made, added
}

// Produced reports whether a resource is one nobody wrote.
func (l Lowered) Produced(id string) bool {
	_, ok := l.Resources[id]
	return ok
}

// LoweredOf reads the record off a compiled object. An object that was not lowered — most
// of them — has none, and the zero value answers "nothing was lowered" to every question.
func LoweredOf(flat map[string]any) Lowered {
	out := Lowered{Resources: map[string]Provenance{}}
	rec, ok := flat[LoweredKey].(map[string]any)
	if !ok {
		return out
	}
	if resources, ok := rec["resources"].(map[string]any); ok {
		for id, v := range resources {
			m, _ := v.(map[string]any)
			out.Resources[id] = Provenance{
				From: str(m, "from"), At: str(m, "at"),
				Kind: str(m, "kind"), Name: str(m, "name"), Scope: str(m, "scope"),
			}
		}
	}
	if entries, ok := rec["entries"].([]any); ok {
		for _, v := range entries {
			m, _ := v.(map[string]any)
			out.Entries = append(out.Entries, Granted{
				On: str(m, "on"), Kind: str(m, "kind"), Name: str(m, "name"),
				Op: str(m, "op"), From: str(m, "from"),
			})
		}
	}
	return out
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// recordOf is what the compile just did, in the terms the object records: ids, because a
// name is only unique in a scope and an object outlives the names in it.
//
// The accessor of an added entry is looked up in the COMPILED object rather than carried
// from the plan — the plan knows it as a name in a scope, which is the same lookup the
// compiler already did, and doing it again here against the result is the only way to be
// sure the two agree.
func recordOf(obj Object, docs []placement, adds []granted) Lowered {
	l := Lowered{Resources: make(map[string]Provenance, len(docs))}
	for _, d := range docs {
		l.Resources[d.id] = Provenance{From: d.from, At: d.part, Kind: d.kind, Name: d.name, Scope: d.scope}
	}

	// Scopes nest, and a name is only unique inside one: keyed without the scope, an
	// entry on `shop/eu/function/relay` was recorded against whichever `relay` the map
	// happened to yield.
	byName := map[string]string{}
	flat := obj.Flat()
	if inner, ok := flat["object"].(map[string]any); ok {
		flat = inner
	}
	var walk func(scope map[string]any, at string)
	walk = func(scope map[string]any, at string) {
		for group, v := range scope {
			instances, ok := v.(map[string]any)
			if !ok {
				continue
			}
			for id, r := range instances {
				res, ok := r.(map[string]any)
				if !ok {
					continue
				}
				name, _ := res[engine.FieldName].(string)
				if name == "" {
					continue
				}
				byName[at+"\x00"+group+"\x00"+name] = id
				child := name
				if at != "" {
					child = at + "/" + name
				}
				walk(res, child)
			}
		}
	}
	walk(flat, "")

	for _, g := range adds {
		id, found := byName[g.scope+"\x00"+g.on.Kind+"\x00"+g.on.Name]
		if !found {
			// The accessor does not exist. That is a fault in what was written — the
			// audit reports it against the resource whose lowering named it — and a
			// record of an entry on nothing would be a lie about where it landed.
			continue
		}
		l.Entries = append(l.Entries, Granted{On: id, Kind: g.kind, Name: g.name, Op: g.op, From: g.from})
	}
	return l
}

// record writes the lowering beside the compiled object.
func record(obj Object, l Lowered) error {
	rec := object.New[object.Refrence]()

	resources := object.New[object.Refrence]()
	ids := make([]string, 0, len(l.Resources))
	for id := range l.Resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := l.Resources[id]
		one := object.New[object.Refrence]()
		one.Set("from", p.From)
		if p.At != "" {
			one.Set("at", p.At)
		}
		one.Set("kind", p.Kind)
		one.Set("name", p.Name)
		if p.Scope != "" {
			one.Set("scope", p.Scope)
		}
		if err := resources.Child(id).Add(one); err != nil {
			return err
		}
	}
	if err := rec.Child("resources").Add(resources); err != nil {
		return err
	}

	entries := make([]any, 0, len(l.Entries))
	for _, g := range l.Entries {
		entries = append(entries, map[string]any{
			"on": g.On, "kind": g.Kind, "name": g.Name, "op": g.Op, "from": g.From,
		})
	}
	if len(entries) > 0 {
		rec.Set("entries", entries)
	}
	return obj.Child(LoweredKey).Add(rec)
}
