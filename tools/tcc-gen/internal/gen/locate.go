package gen

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// Where a field's value sits in a compiled object. The struct model is for editing
// authored config; a reader of a compiled object needs the path, and the two differ
// where a matcher captures a branch: `object: {size: 1GB}` compiles to `{type:
// "object", size: 1000000000}`. The attribute's Segments, Captures and Path, whose
// finite matchers enumerate, say all of it.

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

	// Defaulted marks a field the compiler fills in when the author did not.
	// Its presence in a compiled object says nothing about what was written.
	Defaulted bool

	// Enum is the closed set of values the field may hold — an InSet(), or the
	// branches a captured Either() records.
	Enum []string
}

// Path is the compiled key, joined — the attribute's own identity.
func (l Located) Path() string { return strings.Join(l.Key, "/") }

// Locations is every field of a kind, sorted by compiled key. It stops at kinds the
// node contains (a top-level group is a kind it holds, not a field it has) and at
// nested groups (`ports`, `transformers`), which Groups answers.
func Locations(root []*engine.Node, kind string) []Located {
	iter := kindNode(root, kind)
	if iter == nil {
		return nil
	}
	fields, _ := shapeOf(iter, heldKinds(root))
	return fields
}

// Group is a nested group a node declares: entries keyed by name (`ports`) or
// held in order (`transformers`), each one carrying Fields and Groups of its own.
type Group struct {
	// Key is the group's own key in the compiled object.
	Key string

	// List marks a sequence: the entries are ordered and have no names.
	List bool

	// At is the blocks the group sits under, from its owner: `build` for a package's
	// `build: {steps: [...]}`. Empty for a group at the owner's own root.
	At []string

	Fields []Located
	Groups []Group
}

// Groups is every nested group a kind declares, sorted by key.
func Groups(root []*engine.Node, kind string) []Group {
	iter := kindNode(root, kind)
	if iter == nil {
		return nil
	}
	_, groups := shapeOf(iter, heldKinds(root))
	return groups
}

func heldKinds(root []*engine.Node) map[string]bool {
	held := map[string]bool{}
	for _, g := range root {
		if name, ok := g.Match.(string); ok {
			held[name] = true
		}
	}
	return held
}

func kindNode(root []*engine.Node, kind string) *engine.Node {
	for _, g := range root {
		name, _ := g.Match.(string)
		if name == kind && len(g.Children) > 0 {
			return g.Children[0]
		}
	}
	return nil
}

// shapeOf is a node's own fields, and the nested groups beside them.
func shapeOf(node *engine.Node, held map[string]bool) ([]Located, []Group) {
	seen := map[string]bool{}
	var fields []Located
	var groups []Group
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
			fields = append(fields, locate(a))
		}
		for _, c := range n.Children {
			name := childName(c)
			if held[name] {
				continue
			}
			// A BLOCK IS MORE FIELDS OF THIS NODE, one key down, and its groups sit
			// under that key.
			if engine.IsBlock(c) && name != "" {
				f, g := shapeOf(c, held)
				for _, l := range f {
					fields = append(fields, under(name, l))
				}
				for _, sub := range g {
					sub.At = append([]string{name}, sub.At...)
					groups = append(groups, sub)
				}
				continue
			}
			// A GROUP WITH AN ITERATOR IS ENTRIES, not more fields of this node.
			if c.Group && name != "" && len(c.Children) == 1 && !c.Children[0].Group {
				f, g := shapeOf(c.Children[0], held)
				groups = append(groups, Group{Key: name, List: c.Sequence, Fields: f, Groups: g})
				continue
			}
			walk(c)
		}
	}
	walk(node)

	sort.Slice(fields, func(i, j int) bool { return fields[i].Path() < fields[j].Path() })
	sort.Slice(groups, func(i, j int) bool { return groups[i].Path() < groups[j].Path() })
	return fields, groups
}

// Path is where the group sits from its owner, its blocks and its key, joined.
func (g Group) Path() string { return strings.Join(append(append([]string(nil), g.At...), g.Key), "/") }

// under is a block's field addressed from the node holding the block.
func under(block string, l Located) Located {
	l.Key = append([]string{block}, l.Key...)
	l.Authored = append([]string{block}, l.Authored...)
	return l
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

		Defaulted: a.Default != nil,
	}
	if enum, ok := a.Meta["enum"].([]string); ok {
		l.Enum = append([]string(nil), enum...)
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
	if l.Captures && len(l.Enum) == 0 {
		// THE BRANCH TAKEN IS THE VALUE, so the branches are the set.
		l.Enum = append([]string(nil), l.Branch...)
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
