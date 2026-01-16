package engine

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

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
}

type Option func(*Attribute)

type Node struct {
	Group      bool
	Match      StringMatch
	Attributes []*Attribute
	Children   []*Node
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
