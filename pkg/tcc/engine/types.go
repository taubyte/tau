package engine

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// IDENTITY: which attribute is a resource's id, and which field receives the name of the
// document it was written in.
//
// The interpreter keys a compiled resource by the first and resolves references against
// the second, so it has to know both — and it used to know them as the string literals
// "id" and "name", in a dozen places, which is one vocabulary's choice written into the
// layer that must not have one. Naming them as constants only made the assumption easier
// to find; the schema says it now.
//
// Stated ONCE, on the root, because it is a convention of the whole vocabulary rather
// than of any kind in it.
func Identity(id, name string) NodeOption {
	return GroupAnnotate("identity", [2]string{id, name})
}

// IdentityOf reads it back. Empty when the schema does not say, which means no resource
// is keyed by an id and none is given a name — a schema of plain documents.
func IdentityOf(root *Node) (id, name string) {
	if root == nil {
		return "", ""
	}
	pair, _ := root.Meta["identity"].([2]string)
	return pair[0], pair[1]
}

type instance struct {
	schema *schemaDef
	seer   *yaseer.Seer
	// vars is every variable the vocabulary declares, in its order, with the closed set
	// of values each may take in THIS project — the whole set of suffixes a document may
	// carry. WHICH value each variable holds for a given read is an argument to
	// ProcessFor.
	vars Vars
}

// Var is one variable and the values it may take in this project. The name comes from
// the vocabulary (Varying, on the root); the values come from the caller, because they
// are a property of the project being read rather than of the language it is written in.
type Var struct {
	Name   string
	Values []string
}

// Vars is every variable, IN THE ROOT'S ORDER. Built by the engine rather than handed to
// it, so precedence is the vocabulary's to state and never a caller's to choose by the
// order it happened to pass its options in.
type Vars []Var

// Values is the closed set this variable may take, nil when it is not one of ours.
func (v Vars) Values(name string) []string {
	for _, each := range v {
		if each.Name == name {
			return each.Values
		}
	}
	return nil
}

// Has reports whether this project declares any value at all for the variable. A
// variable with no values has no variant keys, so binding one is refused rather than
// read as the bare key — see NewWith.
func (v Vars) Has(name string) bool {
	for _, each := range v {
		if each.Name == name {
			return len(each.Values) > 0
		}
	}
	return false
}

// Bound is what the variables ARE for one read: one value per variable, and a variable
// absent from it reads the bare key. It is a map rather than a slice because a caller
// says what it knows; the ORDER the engine resolves them in comes from the root.
type Bound map[string]string

type schemaDef struct {
	root *Node
}

type Schema interface {
	rootNode() *Node
}

type Engine interface {
	Process() (object.Object[object.Refrence], error) // load & process
	// ProcessFor is Process UNDER ONE BINDING: an attribute that varies takes its
	// `<key>_<value>` spelling where the document writes one. An argument rather than
	// instance state, because one parse serves every binding and they share the parsed
	// documents.
	ProcessFor(b Bound) (object.Object[object.Refrence], error)
	Dump(obj object.Object[object.Refrence]) error // dump object to filesystem using engine's seer and sync
}

type Type int

type SupportedTypes interface {
	int | bool | float64 | string | []string
}

const (
	TypeInt Type = iota
	TypeBool
	TypeFloat
	TypeString
	TypeStringSlice
)

type StringMatch any // string or PathMatcher

type AttributeValidator func(any) error

type Attribute struct {
	// Name is the attribute's identity: its key, joined by "/" when the key has more
	// than one segment. Cross-references (ShowWhen, RequiredWhen, KindFrom, a lowering's
	// templates) address an attribute by it.
	Name string
	// Segments is the key as declared — one level of the compiled object per entry, so
	// `Key("execution", "call")` compiles into execution -> call rather than onto a flat
	// leaf that spells its own path. Empty means a single-segment key, which is Name.
	Segments  []string
	Type      Type
	Required  bool
	Captures  bool // this attribute captures WHICH branch of an Either() was taken
	Default   any
	Path      []StringMatch
	Validator AttributeValidator
	Meta      map[string]any // opaque codegen metadata; the engine never interprets it
}

type Option func(*Attribute)

type Node struct {
	// Leaf is the document this group's instances keep their own fields in —
	// `namespace` gives namespaces/shop/namespace.yaml. Empty means
	// the document a container instance keeps its own fields in; see Leaf.
	//
	// It exists because that name was a GLOBAL, and a global reserved the word
	// everywhere: a nested group called `config` under a resource was silently
	// skipped, because somewhere else in the tree a container keeps its fields in a
	// file by that name. Naming it per group makes the reservation local to the group
	// that actually has one, and puts the layout in the DSL with everything else about
	// the layout.
	Leaf  string
	Group bool
	// Nested marks a RESOURCE iterator that also carries child groups, authored
	// inside its own document. Distinct from Group, whose instances are scopes:
	// a nested group is data belonging to the instance, so the instance keeps its
	// scalar parsing, its wire-key renames and its name->id index, and names inside
	// the group resolve where the resource was authored.
	Nested bool
	// Sequence marks a group authored as a YAML SEQUENCE rather than a mapping —
	// an ORDERED list of objects. Its instances have no names, only positions, so
	// it compiles to one ordered value instead of children keyed by name.
	//
	// It is a group like any other underneath: each element is loaded through the
	// same iterator, so per-field types, defaults, required, validators, unknown-key
	// rejection and per-field error locations all work inside an element. The only
	// difference is where the key comes from — the author writes the order, the
	// engine reads it back as the index.
	Sequence bool
	// Required marks a group that must be authored with at least one entry.
	Required   bool
	Match      StringMatch
	Attributes []*Attribute
	Children   []*Node
	Meta       map[string]any // opaque codegen metadata; the engine never interprets it
}
