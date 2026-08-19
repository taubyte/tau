package schema

import (
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
)

// DSL builder re-exports, so definition.go reads as bare String(...)/DefineGroup(...)
// without a dot-import (which would collide with this package's own New/Option).
// Same list as v1's, minus the terms the v2 vocabulary deletes with smartops and
// the trigger block (AttachesToAll, DerivedBool, OnlyWhen, SectionWhen).
type (
	Node       = engine.Node
	Attribute  = engine.Attribute
	NodeOption = engine.NodeOption
)

const GenCID = engine.GenCID

const DeriveDNSLabel = engine.DeriveDNSLabel

const DeriveSRVName = engine.DeriveSRVName

var (
	Accessor        = engine.Accessor
	Bool            = engine.Bool
	Bytes           = engine.Bytes
	DefineGroup     = engine.DefineGroup
	DefineIter      = engine.DefineIter
	DefineIterGroup = engine.DefineIterGroup
	DefineList      = engine.DefineList

	// The lowering terms. They live in interp rather than engine because the
	// interpreter is what expands them; they are re-exported here so a vocabulary
	// reads in one language.
	Lowers           = interp.Lowers
	Into             = interp.Into
	PerEach          = interp.PerEach
	When             = interp.When
	Acts             = interp.Acts
	Named            = interp.Named
	Carries          = interp.Carries
	Sets             = interp.Sets
	LinksTo          = interp.LinksTo
	InScope          = interp.InScope
	From             = interp.From
	LinkEffect       = interp.Effect
	AsReference      = interp.AsReference
	Doc              = engine.Doc
	Duration         = engine.Duration
	Either           = engine.Either
	AnswersAt        = engine.AnswersAt
	ByName           = engine.ByName
	Inbound          = engine.Inbound
	KindFrom         = engine.KindFrom
	Float            = engine.Float
	EmitValidation   = engine.EmitValidation
	EnumBool         = engine.EnumBool
	Field            = engine.Field
	Generated        = engine.Generated
	GroupAnnotate    = engine.GroupAnnotate
	GroupDoc         = engine.GroupDoc
	Icon             = engine.Icon
	Int              = engine.Int
	IsCID            = engine.IsCID
	IsFqdn           = engine.IsFqdn
	IsVariableName   = engine.IsVariableName
	Key              = engine.Key
	MinInt           = engine.MinInt
	Nested           = engine.Nested
	NoAccessors      = engine.NoAccessors
	NoGetter         = engine.NoGetter
	NoSetter         = engine.NoSetter
	NoStructField    = engine.NoStructField
	Path             = engine.Path
	Prefix           = engine.Prefix
	Ref              = engine.Ref
	RepoBranch       = engine.RepoBranch
	RepoName         = engine.RepoName
	Required         = engine.Required
	RequiredUnless   = engine.RequiredUnless
	RequiredWhen     = engine.RequiredWhen
	Resource         = engine.Resource
	Root             = engine.Root
	SchemaDefinition = engine.SchemaDefinition
	InSection        = engine.InSection
	Section          = engine.Section
	ShowWhen         = engine.ShowWhen
	Singular         = engine.Singular
	Leaf             = engine.Leaf
	Derived          = engine.Derived
	Strict           = engine.Strict
	String           = engine.String
	StringShape      = engine.StringShape
	StringSlice      = engine.StringSlice
	Tag              = engine.Tag
	WireDrop         = engine.WireDrop
)

// Generic builders can't be re-exported as plain values.
func Default[T any](val T) engine.Option                      { return engine.Default(val) }
func Validator[T any](validator func(T) error) engine.Option  { return engine.Validator(validator) }
func InSet[T string | int | float64](elms ...T) engine.Option { return engine.InSet(elms...) }
