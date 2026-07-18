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

// DerivedBools declares extra bool struct fields a transform pass synthesizes
// with no source attribute (e.g. Function.Secure from type=="https"). The
// generator emits `<Name> bool`, decoded from the compiled key lower(Name).
// Generation-only; no runtime effect.
func DerivedBools(names ...string) NodeOption {
	return GroupAnnotate("derivedBools", names)
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

// StructBool declares that a transform pass projects this attribute's value into
// a bool struct field named goName (e.g. network-access -> Local/Public), decoded
// from the compiled key lower(goName). It replaces the attribute's own struct
// field. Generation-only; no runtime effect.
func StructBool(goName string) Option {
	return Annotate("structBool", goName)
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
