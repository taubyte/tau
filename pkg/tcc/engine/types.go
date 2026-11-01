package engine

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// Identity names which attribute is a resource's id and which field receives the
// name of the document it was written in, once on the root: the interpreter keys
// resources by the first and resolves references against the second.
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
	// vars is every declared variable, in order, with the values each may take in
	// this project; which value each holds for a read is ProcessFor's argument.
	vars Vars
}

// Var is one variable and the values it may take in this project. The name comes from
// the vocabulary (Varying, on the root); the values come from the caller, because they
// are a property of the project being read rather than of the language it is written in.
type Var struct {
	Name   string
	Values []string
}

// Vars is every variable in the root's order, so precedence is the vocabulary's.
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
	// ProcessFor is Process under one binding; an argument because one parse serves
	// every binding.
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
	// Leaf is the document this group's instances keep their own fields in
	// (`namespace` gives namespaces/shop/namespace.yaml). Per group, so the name is
	// reserved only where a group has one.
	Leaf  string
	Group bool
	// Nested marks a resource iterator that also carries child groups authored in
	// its own document. Unlike Group, a nested group is data of the instance, so names
	// in it resolve where the resource was authored.
	Nested bool
	// Sequence marks a group authored as a YAML sequence: an ordered list of objects
	// with positions, not names, compiled to one ordered value. Each element loads
	// through the same iterator.
	Sequence bool
	// Required marks a group that must be authored with at least one entry.
	Required   bool
	Match      StringMatch
	Attributes []*Attribute
	Children   []*Node
	Meta       map[string]any // opaque codegen metadata; the engine never interprets it
}
