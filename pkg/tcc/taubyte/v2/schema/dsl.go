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
	Node      = engine.Node
	Attribute = engine.Attribute
	// AttrOption is engine.Option under a name that does not collide with this
	// package's own compiler Option. It is here so a shared attribute helper can
	// take one caller's demand as an argument — wasmAttributesEntrypoint does,
	// because a worker's entrypoint is required only on the wasm tier.
	AttrOption = engine.Option
	NodeOption = engine.NodeOption
)

const GenCID = engine.GenCID

const DeriveDNSLabel = engine.DeriveDNSLabel

const DeriveSRVName = engine.DeriveSRVName

var (
	Accessor        = engine.Accessor
	DefineGroup     = engine.DefineGroup
	DefineIter      = engine.DefineIter
	DefineIterGroup = engine.DefineIterGroup
	DefineList      = engine.DefineList

	// The lowering terms. They live in interp rather than engine because the
	// interpreter is what carries them out; they are re-exported here so a vocabulary
	// reads in one language.
	Lowers     = interp.Lowers
	Lowerable  = interp.Lowerable
	Into       = interp.Into
	PerEach    = interp.PerEach
	Acts       = interp.Acts
	Named      = interp.Named
	Carries    = interp.Carries
	Writes     = interp.Writes
	On         = interp.On
	At         = interp.At
	AtProduced = interp.AtProduced

	// The VALUE terms — what a lowering says a name, a field or a written value is made
	// of. `Attr` is the resource being lowered, `Each` the element, `Next` the one after
	// it, `Scope` where the produced resources were put, `Text` fixed text and `Concat`
	// several of those in a row. Attr/Each/Next are generic over the key spelling, so
	// they are functions below rather than values here.
	Text             = interp.Text
	Concat           = interp.Concat
	Scope            = interp.Scope
	Doc              = engine.Doc
	Either           = engine.Either
	AnswersAt        = engine.AnswersAt
	ByName           = engine.ByName
	Serves           = engine.Serves
	Inbound          = engine.Inbound
	EmitValidation   = engine.EmitValidation
	EnumBool         = engine.EnumBool
	Field            = engine.Field
	Generated        = engine.Generated
	GroupAnnotate    = engine.GroupAnnotate
	GroupDoc         = engine.GroupDoc
	HttpMethods      = engine.HttpMethods
	Icon             = engine.Icon
	IsCID            = engine.IsCID
	IsFqdn           = engine.IsFqdn
	IsVariableName   = engine.IsVariableName
	Captures         = engine.Captures
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
	PerEnv           = engine.PerEnv
	Resource         = engine.Resource
	Root             = engine.Root
	SchemaDefinition = engine.SchemaDefinition
	InSection        = engine.InSection
	Section          = engine.Section
	Singular         = engine.Singular
	Identity         = engine.Identity
	Leaf             = engine.Leaf
	Derived          = engine.Derived
	Strict           = engine.Strict
	StringShape      = engine.StringShape
	Tag              = engine.Tag
	WireDrop         = engine.WireDrop
)

// Generic builders can't be re-exported as plain values.
func Default[T any](val T) engine.Option                      { return engine.Default(val) }
func Validator[T any](validator func(T) error) engine.Option  { return engine.Validator(validator) }
func InSet[T string | int | float64](elms ...T) engine.Option { return engine.InSet(elms...) }

// The typed attribute constructors take EITHER a plain string key or a multi-segment
// Key(...), so they are generic and cannot be re-exported as values like the rest.
type AKey = engine.AKey

func Key(segments ...string) AKey { return engine.Key(segments...) }

// When gates a lowering on one of the lowered kind's attributes, by key.
func When[T string | AKey](field T) interp.LoweringOption { return interp.When(field) }

// Sets names one of the produced kind's attributes, by key.
func Sets[T string | AKey](field T, value interp.Term) interp.LoweringOption {
	return interp.Sets(field, value)
}

// Attr, Each and Next name an attribute a lowering READS — of the resource being lowered,
// of the element it iterates, and of the element after that one. They take the same two
// spellings every other term that names an attribute takes, a plain key or Key(...)
// segments, so they are generic and cannot be re-exported as values.
func Attr[T string | AKey](field T) interp.Term { return interp.Attr(field) }
func Each[T string | AKey](field T) interp.Term { return interp.Each(field) }
func Next[T string | AKey](field T) interp.Term { return interp.Next(field) }

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

// The terms that NAME another attribute take either a plain key or a multi-segment
// Key(...), so they are generic and cannot be re-exported as values.
func ShowWhen[T string | AKey](field T, in ...string) engine.Option {
	return engine.ShowWhen(field, in...)
}
func RequiredWhen[T string | AKey](field T, in ...string) engine.Option {
	return engine.RequiredWhen(field, in...)
}
func RequiredUnless[T string | AKey](field T) engine.Option { return engine.RequiredUnless(field) }
func KindFrom[T string | AKey](field T) engine.RefOpt       { return engine.KindFrom(field) }
