package engine

import (
	"fmt"
	"strings"
)

// AKey is a compiled key with more than one segment — one level of the object per
// entry. Built with Key(...); an attribute whose key is a single segment is declared
// with a plain string, as it always was.
type AKey []string

// Key declares the key an attribute compiles to, in segments:
//
//	String(Key("execution", "call"))
//
// A key may never contain "/" (keys.go). It is authored at the same place unless
// Path(...) says otherwise.
func Key(segments ...string) AKey { return AKey(segments) }

// FieldName is how one term names another attribute: a plain key, or a nested
// one's segments joined. The engine writes that "/"; a vocabulary never does.
func FieldName[T string | AKey](field T) string {
	switch f := any(field).(type) {
	case string:
		return f
	case AKey:
		return strings.Join(f, "/")
	}
	return ""
}

func keyOf[T string | AKey](name T) (string, []string) {
	switch k := any(name).(type) {
	case string:
		mustNotSpellAPath("attribute", k)
		return k, nil
	case AKey:
		if len(k) == 0 {
			panic("Key() declares no segments")
		}
		for _, seg := range k {
			mustBeKey("attribute key segment", seg)
		}
		if len(k) == 1 {
			return k[0], nil
		}
		return strings.Join(k, "/"), k
	}
	panic("unreachable")
}

func defAttr[T string | AKey](name T, _type Type, options []Option) *Attribute {
	id, segments := keyOf(name)
	a := &Attribute{
		Name:     id,
		Segments: segments,
		Type:     _type,
	}

	for _, opt := range options {
		opt(a)
	}

	// Authored where it compiles unless Path says otherwise. Resolved here because
	// the strict key check reads Path before any document is opened.
	if len(a.Path) == 0 {
		a.Path = make([]StringMatch, 0, len(segments)+1)
		if len(segments) == 0 {
			a.Path = append(a.Path, a.Name)
		}
		for _, seg := range segments {
			a.Path = append(a.Path, seg)
		}
	}

	return a
}

func Int[T string | AKey](name T, options ...Option) *Attribute {
	return defAttr(name, TypeInt, options)
}

func Bool[T string | AKey](name T, options ...Option) *Attribute {
	return defAttr(name, TypeBool, options)
}

func Float[T string | AKey](name T, options ...Option) *Attribute {
	return defAttr(name, TypeFloat, options)
}

func String[T string | AKey](name T, options ...Option) *Attribute {
	return defAttr(name, TypeString, options)
}

func StringSlice[T string | AKey](name T, options ...Option) *Attribute {
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
	mustBeKey("group", match)
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
		// Marking which children belong to the instance's own document lets the loader
		// and dumper route a resource that is also a container: nested data in its leaf
		// document, resource groups in directories below it.
		for _, child := range children {
			child.Nested = true
		}
		n.Children = append(n.Children, children...)
	}
}

// DefineList declares an ordered list of objects, a group authored as a YAML
// sequence:
//
//	DefineList("transformers", []*Attribute{
//	    String("namespace"), String("name", Required()),
//	})
//
//	transformers:
//	    - name: redact
//	    - namespace: /shop
//	      name: tier_header
//
// For a pipeline, where order means something. A name-keyed group is the better
// default: a name is a handle, an index is not.
func DefineList(name string, attrs []*Attribute, extra ...any) *Node {
	mustBeKey("list", name)
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

// listUnsupported names the terms whose work happens above the engine, in the
// per-instance passes a list element (one ordered value) never gets: a Duration
// would stay "50ms" and a Ref a name. Drawn from the Meta keys the interpreter
// dispatches on.
func listUnsupported(a *Attribute) (string, bool) {
	for _, term := range []string{"scalar", "ref"} {
		if _, ok := a.Meta[term]; !ok {
			continue
		}
		// A ByName reference needs no pass: the resolver skips it wherever it appears.
		if term == "ref" {
			if spec, ok := a.Meta["ref"].(RefSpec); ok && spec.ByName {
				continue
			}
		}
		return term + "()", true
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

// Leaf names the document this group's instances keep their own fields in.
func Leaf(name string) NodeOption {
	mustBeKey("leaf", name)
	return func(n *Node) { n.Leaf = name }
}

// LeafOf is the document a group's instances keep their own fields in, as Leaf()
// declares, or "". Exported so a consumer mapping an address to a file does not
// re-derive the convention.
func LeafOf(n *Node) string {
	if n == nil {
		return ""
	}
	return n.Leaf
}

func (n *Node) With(opts ...NodeOption) *Node {
	for _, o := range opts {
		o(n)
	}
	return n
}

// AnswersAt declares the block where a kind says what name it answers on:
// `<prefix>/kind` the switch, `<prefix>/name` the label, `<prefix>/zone` the
// zone. It lets a drawing put the internet in front of the right resources.
func AnswersAt(prefix string) NodeOption { return GroupAnnotate("answersAt", prefix) }

// AnswersAtOf reports the naming block of a kind, or "".
func AnswersAtOf(n *Node) string {
	if n == nil {
		return ""
	}
	p, _ := n.Meta["answersAt"].(string)
	return p
}
