package engine

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// NodeDefaultSeerLeaf is the document a container's instances keep their OWN fields in
// when the DSL does not say. A group can name its own with Leaf(...), and should: a
// package-level default is a layout rule that the schema — the thing whose whole job is
// to describe the layout — does not state.
var NodeDefaultSeerLeaf = "config"

type instance struct {
	schema *schemaDef
	seer   *yaseer.Seer
}

type schemaDef struct {
	root *Node
}

type Schema interface {
	Yaml() (string, error)
	Json() (string, error)
	Map() map[string]any
}

type Engine interface {
	Schema() Schema
	Parse() (object.Object[SeerRef], error)           // load linked to seer
	Process() (object.Object[object.Refrence], error) // load & process
	Dump(obj object.Object[object.Refrence]) error    // dump object to filesystem using engine's seer and sync
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
	Name      string
	Type      Type
	Required  bool
	Key       bool // means the value is the key of a map
	Default   any
	Path      []StringMatch
	Compat    []StringMatch
	Validator AttributeValidator
	Meta      map[string]any // opaque codegen metadata; the engine never interprets it
}

type Option func(*Attribute)

type Node struct {
	// Strict rejects an authored key this vocabulary cannot read. Declared on the
	// root; see strict.go for why it is not simply always on.
	Strict bool

	// Leaf is the document this group's instances keep their own fields in —
	// `namespace` gives namespaces/shop/namespace.yaml. Empty means
	// NodeDefaultSeerLeaf.
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
	Nested     bool
	Match      StringMatch
	Attributes []*Attribute
	Children   []*Node
	Meta       map[string]any // opaque codegen metadata; the engine never interprets it
}

type SeerOps struct {
	ops []*yaseer.Query
}

type SeerRef *struct {
	object.Object[object.Refrence]
	ops   *SeerOps
	query *yaseer.Query
}

type ObjectDataType interface {
	object.Refrence | SeerRef
}
