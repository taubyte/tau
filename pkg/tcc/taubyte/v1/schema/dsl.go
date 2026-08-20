package schema

import "github.com/taubyte/tau/pkg/tcc/engine"

// DSL builder re-exports. definition.go used to `. "engine"` dot-import these so
// the schema DSL reads as bare String(...)/DefineGroup(...)/etc. That dot-import
// pulled engine's exported New and Option into the file block, which collided with
// the facade's own New/Option (facade.go) at the package block — Go forbids an
// identifier in both the file and package block. Re-exporting only the builders the
// DSL actually names (never New/Option) keeps definition.go's call sites unchanged
// while letting the schema package own New/Option for the public facade.
type (
	Node      = engine.Node
	Attribute = engine.Attribute
)

// Known value generators (see engine.Generated).
const GenCID = engine.GenCID

var (
	Accessor         = engine.Accessor
	Addressing       = engine.Addressing
	AttachesToAll    = engine.AttachesToAll
	Compat           = engine.Compat
	DefineGroup      = engine.DefineGroup
	DefineIter       = engine.DefineIter
	DefineIterGroup  = engine.DefineIterGroup
	DerivedBool      = engine.DerivedBool
	Doc              = engine.Doc
	Either           = engine.Either
	Embeds           = engine.Embeds
	EmitValidation   = engine.EmitValidation
	EnumBool         = engine.EnumBool
	Field            = engine.Field
	Generated        = engine.Generated
	GroupDoc         = engine.GroupDoc
	Icon             = engine.Icon
	IsCID            = engine.IsCID
	IsEmail          = engine.IsEmail
	IsFqdn           = engine.IsFqdn
	IsHttpMethod     = engine.IsHttpMethod
	IsVariableName   = engine.IsVariableName
	Captures         = engine.Captures
	NoAccessors      = engine.NoAccessors
	NoGetter         = engine.NoGetter
	NoSetter         = engine.NoSetter
	NoStructField    = engine.NoStructField
	OnlyWhen         = engine.OnlyWhen
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
	SectionWhen      = engine.SectionWhen
	ShowWhen         = engine.ShowWhen
	Singular         = engine.Singular
	Leaf             = engine.Leaf
	Identity         = engine.Identity
	StringShape      = engine.StringShape
	Tag              = engine.Tag
	WireDrop         = engine.WireDrop
)

// Generic builders can't be re-exported as plain values; thin wrappers preserve the
// bare-name call sites in definition.go.
func Default[T any](val T) engine.Option                      { return engine.Default(val) }
func Validator[T any](validator func(T) error) engine.Option  { return engine.Validator(validator) }
func InSet[T string | int | float64](elms ...T) engine.Option { return engine.InSet(elms...) }

// The typed attribute constructors take EITHER a plain string key or a multi-segment
// Key(...), so they are generic and cannot be re-exported as values like the rest.
type AKey = engine.AKey

func Key(segments ...string) AKey { return engine.Key(segments...) }

func String[T string | AKey](name T, o ...engine.Option) *Attribute { return engine.String(name, o...) }
func StringSlice[T string | AKey](name T, o ...engine.Option) *Attribute {
	return engine.StringSlice(name, o...)
}
func Int[T string | AKey](name T, o ...engine.Option) *Attribute   { return engine.Int(name, o...) }
func Bool[T string | AKey](name T, o ...engine.Option) *Attribute  { return engine.Bool(name, o...) }
func Float[T string | AKey](name T, o ...engine.Option) *Attribute { return engine.Float(name, o...) }
func Bytes[T string | AKey](name T, o ...engine.Option) *Attribute { return engine.Bytes(name, o...) }
func Duration[T string | AKey](name T, o ...engine.Option) *Attribute {
	return engine.Duration(name, o...)
}
