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

var (
	Accessor              = engine.Accessor
	Addressing            = engine.Addressing
	AttachesToAll         = engine.AttachesToAll
	Bool                  = engine.Bool
	Bytes                 = engine.Bytes
	Compat                = engine.Compat
	DefineGroup           = engine.DefineGroup
	DefineIter            = engine.DefineIter
	DefineIterGroup       = engine.DefineIterGroup
	DerivedBool           = engine.DerivedBool
	Doc                   = engine.Doc
	Duration              = engine.Duration
	Either                = engine.Either
	Embeds                = engine.Embeds
	EmitValidation        = engine.EmitValidation
	EnumBool              = engine.EnumBool
	Field                 = engine.Field
	GroupDoc              = engine.GroupDoc
	IsCID                 = engine.IsCID
	IsEmail               = engine.IsEmail
	IsFqdn                = engine.IsFqdn
	IsHttpMethod          = engine.IsHttpMethod
	IsVariableName        = engine.IsVariableName
	Key                   = engine.Key
	NoAccessors           = engine.NoAccessors
	NoGetter              = engine.NoGetter
	NoSetter              = engine.NoSetter
	NoStructField         = engine.NoStructField
	OnlyWhen              = engine.OnlyWhen
	Path                  = engine.Path
	Prefix                = engine.Prefix
	Ref                   = engine.Ref
	Required              = engine.Required
	Resource              = engine.Resource
	Root                  = engine.Root
	SchemaDefinition      = engine.SchemaDefinition
	Section               = engine.Section
	SectionDefinition     = engine.SectionDefinition
	SectionDefinitionWhen = engine.SectionDefinitionWhen
	ShowWhen              = engine.ShowWhen
	Singular              = engine.Singular
	String                = engine.String
	StringShape           = engine.StringShape
	StringSlice           = engine.StringSlice
	Tag                   = engine.Tag
	WireDrop              = engine.WireDrop
)

// Generic builders can't be re-exported as plain values; thin wrappers preserve the
// bare-name call sites in definition.go.
func Default[T any](val T) engine.Option                      { return engine.Default(val) }
func Validator[T any](validator func(T) error) engine.Option  { return engine.Validator(validator) }
func InSet[T string | int | float64](elms ...T) engine.Option { return engine.InSet(elms...) }
