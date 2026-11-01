package interp

import (
	"sort"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// The compile records what it lowered beside the object, as it does the index,
// so decompile can take it back out; a key on the resources themselves would be
// one the vocabulary does not have. Read it with LoweredOf.

// The shape of a compiled artifact: the project under one key, the index and the
// lowering record beside it. tau reads the first two off every artifact, so they
// are a contract.
const (
	// ProjectKey is where the compiled project sits.
	ProjectKey = "object"
	// IndexesKey is the index subtree beside it.
	IndexesKey = "indexes"
	// LoweredKey is the record of what the compile lowered.
	LoweredKey = "lowered"
)

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

// Granted is one value a lowering wrote into a resource somebody did write: a
// path and a value, nothing about what either means, so decompile can undo it
// for any vocabulary.
type Granted struct {
	// On is the compiled id of the resource that now carries it.
	On string `json:"on"`
	// Path is where inside that resource, and Value is what was put there.
	Path  []string `json:"path"`
	Value string   `json:"value"`
	// From is the compiled id of the authored resource whose lowering wrote it.
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
			g := Granted{On: str(m, "on"), Value: str(m, "value"), From: str(m, "from")}
			for _, seg := range list(m, "path") {
				g.Path = append(g.Path, seg)
			}
			out.Entries = append(out.Entries, g)
		}
	}
	return out
}

// list reads a []string back off a record that may have been through a wire format.
func list(m map[string]any, key string) []string {
	switch v := m[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// recordOf records the compile in ids, since a name is unique only in a scope.
// An added entry's accessor is looked up in the compiled object, so the record
// and the result agree.
func recordOf(root *engine.Node, obj Object, docs []placement, adds []granted) Lowered {
	l := Lowered{Resources: make(map[string]Provenance, len(docs))}
	for _, d := range docs {
		l.Resources[d.id] = Provenance{From: d.from, At: d.part, Kind: d.kind, Name: d.name, Scope: d.scope}
	}

	// Keyed with the scope: a name is unique only inside one.
	_, nameField := engine.IdentityOf(root)
	byName := map[string]string{}
	flat := obj.Flat()
	if inner, ok := flat[ProjectKey].(map[string]any); ok {
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
				name, _ := res[nameField].(string)
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
		l.Entries = append(l.Entries, Granted{On: id, Path: g.path, Value: g.value, From: g.from})
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
		path := make([]any, 0, len(g.Path))
		for _, seg := range g.Path {
			path = append(path, seg)
		}
		entries = append(entries, map[string]any{
			"on": g.On, "path": path, "value": g.Value, "from": g.From,
		})
	}
	if len(entries) > 0 {
		rec.Set("entries", entries)
	}
	return obj.Child(LoweredKey).Add(rec)
}
