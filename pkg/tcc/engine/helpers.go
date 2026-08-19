package engine

import "fmt"

func stringify(a any) string {
	switch b := a.(type) {
	case string:
		return b
	case StringMatcher:
		return b.String()
	case AttributeValidator:
		return "AttributeValidator()"
	case []StringMatch:
		ret := ""
		for i, s := range b {
			ret += stringify(s)
			if i < len(b)-1 {
				ret += "/"
			}
		}
		return ret
	default:
		return "unknown"
	}
}

func defAttr(name string, _type Type, options []Option) *Attribute {
	a := &Attribute{
		Name: name,
		Type: _type,
	}

	for _, opt := range options {
		opt(a)
	}

	return a
}

func Int(name string, options ...Option) *Attribute {
	return defAttr(name, TypeInt, options)
}

func Bool(name string, options ...Option) *Attribute {
	return defAttr(name, TypeBool, options)
}

func Float(name string, options ...Option) *Attribute {
	return defAttr(name, TypeFloat, options)
}

func String(name string, options ...Option) *Attribute {
	return defAttr(name, TypeString, options)
}

func StringSlice(name string, options ...Option) *Attribute {
	return defAttr(name, TypeStringSlice, options)
}

func Attributes(attrs ...*Attribute) []*Attribute {
	return attrs
}

func Root(attrs []*Attribute, children ...*Node) *Node {
	return &Node{
		Group:      true,
		Attributes: attrs,
		Children:   children,
	}
}

func Define(match StringMatch, attrs []*Attribute, children ...*Node) *Node {
	return &Node{
		Group:      false,
		Match:      match,
		Attributes: attrs,
		Children:   children,
	}
}

func DefineGroup(match string, children ...*Node) *Node {
	return &Node{
		Group:    true,
		Match:    match,
		Children: children,
	}
}

func DefineIter(attrs []*Attribute, extra ...any) *Node {
	n := &Node{
		Group:      false,
		Match:      StringMatchAll{},
		Attributes: attrs,
	}
	for _, e := range extra {
		switch v := e.(type) {
		case *Node:
			n.Children = append(n.Children, v)
		case NodeOption:
			v(n)
		}
	}
	return n
}

// Nested attaches child groups authored inside a resource's own document — an
// `edges:`/`caches:` map in functions/checkout.yaml, not a sibling directory.
// Passed to DefineIter alongside its attributes.
func Nested(children ...*Node) NodeOption {
	return func(n *Node) {
		n.Nested = true
		n.Children = append(n.Children, children...)
	}
}

// DefineList declares an ORDERED LIST OF OBJECTS: a group authored as a YAML
// sequence, each element carrying the given attributes.
//
//	DefineList("transformers", []*Attribute{
//	    String("function", Required()), String("fail"),
//	})
//
//	transformers:
//	    - function: redact
//	      fail: closed
//	    - function: /shop/tier_header
//
// Reach for it when the entries are a PIPELINE — when running them in a different
// order means something different. A name-keyed group (DefineGroup + DefineIter) is
// the right shape when entries are addressed rather than sequenced, and it is the
// better default: a name is a handle, an index is not.
//
// The list is a group plus its iterator, the same two nodes any other group is; the
// marker only says where the keys come from.
func DefineList(name string, attrs []*Attribute, extra ...any) *Node {
	for _, a := range attrs {
		if term, ok := listUnsupported(a); ok {
			// Refusing at construction, loudly, beats compiling a list whose
			// elements quietly skip a projection. See the comment on the term list.
			panic(fmt.Sprintf("DefineList(%q): attribute %q declares %s, which does not run inside a list element", name, a.Name, term))
		}
	}
	return &Node{
		Group:    true,
		Sequence: true,
		Match:    name,
		Children: []*Node{DefineIter(attrs, extra...)},
	}
}

// listUnsupported names the terms whose work happens ABOVE the engine — the wire
// projections and ref resolution the interpreter runs per object instance. A list
// element is one ordered VALUE, not an instance, so those passes never see it:
// a Duration inside a list would stay "50ms" where every other Duration is
// nanoseconds, and a Ref would stay a name where every other ref is an id.
//
// Everything the ENGINE does works normally inside an element — type, Required,
// Default, Validator, Path, InSet, unknown-key rejection, per-field locations.
// The line is exactly the layer boundary, so it is drawn from the Meta keys the
// interpreter dispatches on rather than from a hand-kept list of types.
func listUnsupported(a *Attribute) (string, bool) {
	for _, term := range []string{"scalar", "ref", "tag", "enumBool", "derivedBool", "onlyWhen"} {
		if _, ok := a.Meta[term]; ok {
			return term + "()", true
		}
	}
	return "", false
}

func DefineIterGroup(attrs []*Attribute, children ...*Node) *Node {
	return &Node{
		Group:      true,
		Match:      StringMatchAll{},
		Attributes: attrs,
		Children:   children,
	}
}

// With applies node options to an already-built node and returns it, for the
// ctors that take only children (DefineGroup / DefineIterGroup) and so can't
// take options inline. Generation-only metadata; no runtime effect.
// Leaf names the document this group's instances keep their own fields in.
func Leaf(name string) NodeOption {
	return func(n *Node) { n.Leaf = name }
}

// LeafOf is the document a group's instances keep their own fields in, declared or
// defaulted. Exported because a consumer mapping an address to a file — a session, a
// CLI, a UI — must not re-derive the convention.
func LeafOf(n *Node) string {
	if n == nil || n.Leaf == "" {
		return NodeDefaultSeerLeaf
	}
	return n.Leaf
}

func (n *Node) With(opts ...NodeOption) *Node {
	for _, o := range opts {
		o(n)
	}
	return n
}
