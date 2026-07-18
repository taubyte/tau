package engine

// Annotate attaches opaque metadata to an attribute. The engine stores it and
// never interprets it — only code generators (and other out-of-band tooling)
// read it. This is the generic hook that domain-specific vocabulary is built on.
func Annotate(key string, val any) Option {
	return func(a *Attribute) {
		if a.Meta == nil {
			a.Meta = map[string]any{}
		}
		a.Meta[key] = val
	}
}

// Duration and Bytes are String attributes tagged with a scalar-codec name. The
// engine treats them exactly as strings — parsing of "20s"/"32GB" into typed
// values stays in the transform passes. The tag only tells a code generator the
// concrete Go type to emit (e.g. uint64). It has no runtime effect.
func Duration(name string, opts ...Option) *Attribute {
	return String(name, append(opts, Annotate("scalar", "duration"))...)
}

func Bytes(name string, opts ...Option) *Attribute {
	return String(name, append(opts, Annotate("scalar", "bytes"))...)
}

// Field overrides the Go struct field name a generator emits for this attribute,
// for cases where the config-key-derived name differs from the struct field
// (e.g. github-id -> RepoID). Generation-only; no runtime effect.
func Field(goName string) Option {
	return Annotate("field", goName)
}

// Tag sets the mapstructure key a generator emits for this attribute's struct
// field, when the on-disk key isn't derivable from the schema (e.g. github-id ->
// `mapstructure:"repository-id"`). Generation-only; no runtime effect.
func Tag(key string) Option {
	return Annotate("tag", key)
}

// Accessor overrides the exported NAME of the pkg/schema getter/setter a
// generator emits for this attribute, when the config-key-derived name doesn't
// match the existing public API (e.g. fqdn -> FQDN, match -> ChannelMatch).
// Distinct from Field, which names the struct field. Generation-only.
func Accessor(goName string) Option {
	return Annotate("accessor", goName)
}

// NoSetter suppresses the generated pkg/schema setter for this attribute — the
// write is folded into a combined hand-written helper (e.g. Channel/Bridges).
// The getter is still emitted. Generation-only; no runtime effect.
func NoSetter() Option {
	return Annotate("noSetter", true)
}

// NoGetter suppresses the generated pkg/schema getter for this attribute — the
// read applies a value transform the DSL can't express (e.g. fqdn lower-cases).
// The setter is still emitted. Generation-only; no runtime effect.
func NoGetter() Option {
	return Annotate("noGetter", true)
}

// NoAccessors suppresses BOTH generated pkg/schema accessors — neither is
// mechanical (a value transform, combined encryption, or deep github-* folded
// into a hand-written helper own this attribute's config surface). Note: the TS
// source facade is unaffected and still edits these keys. Generation-only.
func NoAccessors() Option {
	return func(a *Attribute) {
		NoSetter()(a)
		NoGetter()(a)
	}
}

// NoStructField declares that this attribute projects to no structureSpec struct
// field (and no TS wire/session field) — folded elsewhere (encryption-type) or
// unimplemented (http-methods). Generation-only; no runtime effect.
func NoStructField() Option {
	return Annotate("noStructField", true)
}

// NodeOption mutates a node — the node-level analogue of Option. Passed to
// DefineIter to attach opaque metadata to the object template (the node each
// compiled resource-object matches), not to the parse rules.
type NodeOption func(*Node)

// GroupAnnotate attaches opaque metadata to a node. Like Annotate, the engine
// stores it and never interprets it — only code generators read it.
func GroupAnnotate(key string, val any) NodeOption {
	return func(n *Node) {
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		n.Meta[key] = val
	}
}

// Capability is an opaque object-addressing tag. The engine requires only that it
// name itself; the meaning of each value (e.g. "wasm" -> WasmModulePath) lives
// entirely in the code generator. It is an interface rather than a string alias so
// callers pass typed capability values, not arbitrary strings, and so richer
// behaviour can be added later without touching the engine.
type Capability interface {
	String() string
}

// Addressing records the set of TNS-key capabilities a compiled object has, for a
// generator to emit its path helpers. Generation-only; no runtime effect.
func Addressing(caps ...Capability) NodeOption {
	return GroupAnnotate("addressing", caps)
}

// Embeds records the structureSpec interface types a generated struct embeds
// (e.g. "Basic", "Indexer", "Wasm") — the object-addressing behaviours it
// exposes. Kept explicit (not derived from Addressing) because a few resources
// embed more than their capability flags imply (e.g. messaging embeds Wasm).
// Generation-only; no runtime effect.
func Embeds(names ...string) NodeOption {
	return GroupAnnotate("embeds", names)
}

// AttachesToAll marks a resource group as cross-cutting: every OTHER compiled
// resource carries a trailing derived []string field listing the instances of
// THIS kind attached to it. The generator names that universal field from this
// group's Resource iface and keys it by this group's config key (e.g. the
// smartops group -> a `SmartOps []string` field on every resource, key
// "smartops"). The compiler synthesizes the list in a pass (from tags), never
// from an authored key — so this is purely generation metadata, no runtime
// effect. Requires Resource(...) on the same node.
func AttachesToAll() NodeOption {
	return GroupAnnotate("attachesToAll", true)
}

// Singular declares the Go type name a container group compiles to (e.g. the
// applications group -> "Application"). Required on any container group (one
// whose iterator holds resource sub-groups); the generator fails loudly rather
// than guess a singular from the plural key. Generation-only; no runtime effect.
func Singular(goName string) NodeOption {
	return GroupAnnotate("singular", goName)
}

// EnumBoolSpec is the compile/decompile contract for an EnumBool attribute.
type EnumBoolSpec struct {
	GoName      string    // bool struct-field name (network-access -> Local/Public)
	TrueWhen    []string  // source values that compile the bool to true
	DropWhen    []string  // source values whose wire key is dropped after projection
	DecompileAs [2]string // [falseVal, trueVal] restored on decompile
}

// EnumBool declares that this enum attribute projects to a bool struct field
// named goName (network-access -> Local/Public), replacing the attribute's own
// field. It carries the full compile/decompile contract a generic driver reads:
// values in trueWhen compile the bool true; values in dropWhen have their wire
// key deleted after projection (values in neither survive compile as-is, e.g. a
// database's authored "subnet"); decompileAs is the [false,true] pair the
// inverse restores. Generation reads only GoName; the rest is inert driver data
// this phase. No runtime effect (the hand-written passes still hardcode this).
func EnumBool(goName string, trueWhen, dropWhen []string, decompileAs [2]string) Option {
	return Annotate("enumBool", EnumBoolSpec{GoName: goName, TrueWhen: trueWhen, DropWhen: dropWhen, DecompileAs: decompileAs})
}

// DerivedBoolSpec is the compile/decompile contract for a DerivedBool attribute.
type DerivedBoolSpec struct {
	GoName      string          // synthesized bool struct-field name (Function.Secure)
	When        map[string]bool // source value -> the bool it yields
	Reconstruct map[bool]string // bool -> the source value restored on decompile
}

// DerivedBool declares an attribute-level derived bool: a bool struct field
// named goName synthesized from THIS attribute's value (Function.Secure from
// type=="https"). `when` maps each source value to the bool it yields;
// `reconstruct` maps the bool back to a source value on decompile. It is
// attribute-level (unlike the removed node-level DerivedBools) so a generic
// driver knows the source attribute. The generator still emits `<goName> bool`
// alongside the source attribute's own field. Inert driver data this phase; the
// generator reads only GoName. No runtime effect.
func DerivedBool(goName string, when map[string]bool, reconstruct map[bool]string) Option {
	return Annotate("derivedBool", DerivedBoolSpec{GoName: goName, When: when, Reconstruct: reconstruct})
}

// OnlyWhenSpec gates a wire-key rename/emit on a sibling attribute's value.
type OnlyWhenSpec struct {
	Attr string
	Vals []string
}

// OnlyWhen makes an attribute's wire projection conditional on a sibling
// attribute's value (p2p-protocol renames to "service" only when type=="p2p").
// Inert driver data — no generation or runtime effect this phase.
func OnlyWhen(attr string, vals ...string) Option {
	return Annotate("onlyWhen", OnlyWhenSpec{Attr: attr, Vals: vals})
}

// RefSpec declares that an attribute value references another resource group,
// so a driver can resolve it to that group's compiled key, optionally under a
// key Prefix.
type RefSpec struct {
	Group  string
	Prefix string
}

// RefOpt tunes a RefSpec.
type RefOpt func(*RefSpec)

// Prefix sets the RefSpec key prefix.
func Prefix(p string) RefOpt {
	return func(s *RefSpec) { s.Prefix = p }
}

// Ref declares that this attribute's value is a reference into another resource
// group. Inert driver data — no generation or runtime effect this phase.
func Ref(group string, opts ...RefOpt) Option {
	s := RefSpec{Group: group}
	for _, o := range opts {
		o(&s)
	}
	return Annotate("ref", s)
}

// ValidationEmit declares a DEFERRED validation (engine.NextValidation) a driver
// should emit for the compiled resource under Key, using the named validator.
type ValidationEmit struct {
	Key       string
	Validator string
}

// EmitValidation declares a deferred validation to attach at compile time
// (domains -> EmitValidation("domain","dns")). Distinct from the load-time
// Validator(). Inert driver data — no effect this phase.
func EmitValidation(key, validator string) Option {
	return Annotate("emitValidation", ValidationEmit{Key: key, Validator: validator})
}

// WireDrop marks an attribute whose compiled wire key a driver should delete
// after consuming it. Inert driver data — no effect this phase.
func WireDrop() Option {
	return Annotate("wireDrop", true)
}

// Resource declares the irregular Go names a resource generates into, so the
// generator needs no hardcoded per-resource table:
//   - schemaPkg: the pkg/schema/<dir> accessor package (usually the group name;
//     "website" for the "websites" group).
//   - iface:     the exported accessor interface, e.g. "Database", "SmartOps".
//   - specType:  the structureSpec type, e.g. "Database", "SmartOp".
//   - specPkg:   the pkg/specs/<dir> addressing-helper package.
//
// Everything else — struct name, receiver, error noun, folder constant, import
// alias, file name — derives from these. Generation-only; no runtime effect.
func Resource(schemaPkg, iface, specType, specPkg string) NodeOption {
	return GroupAnnotate("resource", [4]string{schemaPkg, iface, specType, specPkg})
}
