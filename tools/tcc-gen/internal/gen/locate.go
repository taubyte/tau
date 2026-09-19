package gen

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// locate.go answers where a field's value SITS in a compiled object.
//
// # Why this is not the struct model beside it
//
// Structs() and GenerateTS() project a kind into one field per logical
// attribute, flat, for an editor: the explorer edits AUTHORED config and wants a
// form, so `limits/timeout` is a `Timeout` input and where it lands is the
// compiler's business.
//
// A reader of a COMPILED object wants the opposite. It has the object and needs
// the path — and the two differ exactly where a matcher captures a branch. A
// storage authors `object: {size: 1GB}` and compiles to `{type: "object", size:
// 1000000000}`: one authored block becomes a nested value, a hoisted value and a
// discriminator, and a flat model of field names cannot say which is which.
//
// Everything needed is already on the attribute and none of it is new: Segments
// is the compiled key a level per entry, Captures marks the attribute that
// records which branch was taken, and Path carries the authored location with
// its matchers intact — and a finite matcher enumerates, which the engine says
// in as many words is "what lets a consumer outside this package address a
// branching location instead of giving up on it".

// Located is one field of a kind, as a reader of a compiled object needs it.
type Located struct {
	// Key is the compiled key, one level per segment. This is where the value
	// IS, which is what a reader addresses.
	Key []string

	// Authored is where the value was WRITTEN, with a matcher rendered as its
	// alternatives. It differs from Key wherever a branch was captured, and it
	// is carried so a generated reader can explain itself.
	Authored []string

	// Branch is the alternatives this field lives under, empty when it is
	// unconditional. `size` on a storage is under object OR streaming; reading
	// it without knowing that is reading one store's size as another's.
	Branch []string

	// Captures marks the attribute that RECORDS which branch was taken — the
	// discriminator a reader switches on.
	Captures bool

	// Read is the Resource accessor that decodes this value.
	Read string

	// Required is the vocabulary's, carried so a renderer can tell an absent
	// value from one that cannot be absent.
	Required bool
}

// Path is the compiled key, joined — the attribute's own identity.
func (l Located) Path() string { return strings.Join(l.Key, "/") }

// Locations is every field of a kind, sorted by compiled key.
//
// IT STOPS AT WHAT THE KIND CONTAINS. A scope holds resources of every kind, and
// a lowerable kind's after-shape holds the resources it produces — both by
// making those kinds' own groups children of its node. Walking through them
// reads another resource's fields as this one's: the namespace reader came out
// with 215 fields, one per attribute in the vocabulary, addressing keys that in
// a namespace object are DIRECTORIES of resources rather than values.
//
// What separates the two is already on the root: a child that is a top-level
// group is a kind, and a kind is something this resource holds rather than
// something it has.
func Locations(root []*engine.Node, kind string) []Located {
	held := map[string]bool{}
	for _, g := range root {
		if name, ok := g.Match.(string); ok {
			held[name] = true
		}
	}

	for _, g := range root {
		name, _ := g.Match.(string)
		if name != kind || len(g.Children) == 0 {
			continue
		}

		seen := map[string]bool{}
		var out []Located
		var walk func(n *engine.Node)
		walk = func(n *engine.Node) {
			if n == nil {
				return
			}
			for _, a := range n.Attributes {
				if a == nil || a.Name == "" || seen[a.Name] {
					continue
				}
				seen[a.Name] = true
				out = append(out, locate(a))
			}
			for _, c := range n.Children {
				if held[childName(c)] {
					continue
				}
				walk(c)
			}
		}
		walk(g.Children[0])

		sort.Slice(out, func(i, j int) bool { return out[i].Path() < out[j].Path() })
		return out
	}
	return nil
}

func locate(a *engine.Attribute) Located {
	key := a.Segments
	if len(key) == 0 {
		key = []string{a.Name}
	}

	l := Located{
		Key:      append([]string(nil), key...),
		Captures: a.Captures,
		Required: a.Required,
		Read:     readerFor(a),
	}

	for _, p := range a.Path {
		switch v := p.(type) {
		case string:
			l.Authored = append(l.Authored, v)
		default:
			// A FINITE MATCHER ENUMERATES. One that does not is a location this
			// cannot address, and saying so is better than emitting a reader
			// that looks at the wrong place.
			if m, ok := v.(interface{ Values() []string }); ok {
				l.Branch = m.Values()
				l.Authored = append(l.Authored, "{"+strings.Join(l.Branch, "|")+"}")
				continue
			}
			l.Authored = append(l.Authored, "*")
		}
	}
	if len(l.Authored) == 0 {
		l.Authored = append([]string(nil), l.Key...)
	}
	return l
}

// readerFor is the Resource method that decodes this attribute.
//
// THE SCALAR WINS WHERE THERE IS ONE. A Duration and a Bytes both compile to a
// number, and reading them as one would hand a caller nanoseconds where it
// wanted a duration — the vocabulary's own codec says which.
func readerFor(a *engine.Attribute) string {
	if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok {
		switch strings.ToLower(sc.ID) {
		case "duration":
			return "Duration"
		case "bytes":
			return "Int"
		}
	}
	switch a.Type {
	case engine.TypeStringSlice:
		return "Strings"
	case engine.TypeBool:
		return "Bool"
	case engine.TypeInt:
		return "Int"
	case engine.TypeFloat:
		return "Float"
	default:
		return "String"
	}
}

// childName is a node's group name, or "" for one that is not a group.
func childName(n *engine.Node) string {
	if n == nil {
		return ""
	}
	name, _ := n.Match.(string)
	return name
}
