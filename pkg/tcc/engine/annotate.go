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

// Duration and Bytes are String attributes tagged with a ScalarSpec — the codec
// (authored "20s"/"32GB" <-> typed wire value) and the Go type a generator emits.
// The engine itself treats them exactly as strings; the ScalarSpec is inert data
// the driver (Parse/Format) and tcc-gen (GoType) read, so each scalar's meaning
// lives in one place instead of a switch in every consumer.
func Duration(name string, opts ...Option) *Attribute {
	return String(name, append(opts, Annotate("scalar", ScalarSpec{ID: "duration", GoType: "uint64", Parse: parseDuration, Format: formatDuration}))...)
}

func Bytes(name string, opts ...Option) *Attribute {
	return String(name, append(opts, Annotate("scalar", ScalarSpec{ID: "bytes", GoType: "uint64", Parse: parseBytes, Format: formatBytes}))...)
}

// Doc attaches a human-readable description to an attribute for schema export
// (it becomes JSON Schema `description`). Documentation-only: the engine and
// compiler ignore it. Distinct from the `description` CONFIG attribute some
// resources declare — this documents the field; that one IS a field.
func Doc(text string) Option {
	return Annotate("doc", text)
}

// GroupDoc is the node-level Doc: a human-readable description for a resource or
// container group, surfaced on its schema object. Documentation-only.
func GroupDoc(text string) NodeOption {
	return GroupAnnotate("doc", text)
}

// ConditionSpec is a simple static visibility condition: show the field or section
// only when a sibling attribute (Field) holds one of In. Presentation-only — a
// UI/CLI evaluates it; it never affects parsing or the compiled output. (For
// compile-time wire-projection gating, that's the separate OnlyWhen.)
type ConditionSpec struct {
	Field string
	In    []string
}

// ShowWhen makes a field's display conditional: a UI/CLI shows it only when the
// sibling attribute Field holds one of in. Schema-only, static — no compile or
// parse effect.
func ShowWhen(field string, in ...string) Option {
	return Annotate("showWhen", ConditionSpec{Field: field, In: in})
}

// SectionSpec declares a human-facing section for a resource's fields — how a UI
// or CLI groups them for display. It is presentation-only (no compile effect) and
// does NOT have to align with the authored nesting: a field under one Path can sit
// in a section with a field from another. Membership is explicit (see Section),
// never derived from Path. Title is the short heading; Doc the longer blurb; When
// (optional) shows the whole section only when its condition holds.
type SectionSpec struct {
	ID    string
	Title string
	Doc   string
	When  *ConditionSpec
}

// SectionDefinition declares a display section with an id, a short Title, and a
// longer description (Doc). Repeatable on a node; declaration order is display
// order. Fields join it with Section(id). Schema-only — no compile/parse effect.
func SectionDefinition(id, title, doc string) NodeOption {
	return sectionDef(SectionSpec{ID: id, Title: title, Doc: doc})
}

// SectionDefinitionWhen is SectionDefinition with a static visibility condition:
// the section is shown only when sibling attribute field holds one of in (e.g. the
// HTTP section only when a function's type is "http"/"https"). Schema-only.
func SectionDefinitionWhen(id, title, doc, field string, in ...string) NodeOption {
	return sectionDef(SectionSpec{ID: id, Title: title, Doc: doc, When: &ConditionSpec{Field: field, In: in}})
}

func sectionDef(spec SectionSpec) NodeOption {
	return func(n *Node) {
		if n.Meta == nil {
			n.Meta = map[string]any{}
		}
		s, _ := n.Meta["sections"].([]SectionSpec)
		n.Meta["sections"] = append(s, spec)
	}
}

// Section assigns an attribute to a display section by id (see SectionDefinition),
// telling a UI/CLI which section to render the field under. Explicit and
// independent of the field's Path. Schema-only — no compile or parse effect.
func Section(id string) Option {
	return Annotate("section", id)
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

// MethodSpec is one object-addressing method a capability contributes to a
// generated structureSpec struct: the Go method Name, its Params and Ret (as
// rendered source fragments), and the Args passed to the delegate. ViaTns picks
// the delegate form — <alias>.Tns().<Name>(args) when true, <alias>.<Name>(args)
// when false. "@" in Args expands to the method receiver. Carrying the method
// shape as data is what lets a generator render a capability's methods without a
// term-keyed switch. Generation-only; no runtime effect.
type MethodSpec struct {
	Name   string
	Params string
	Ret    string
	Args   string
	ViaTns bool
}

// MethodCarrier is a Capability that also declares the object-addressing methods
// it generates. A generator reads AddressingMethods() to render them, so the
// capability's codegen meaning lives on the term — the generator needs neither a
// per-capability switch nor a dependency on where the capability is defined.
type MethodCarrier interface {
	Capability
	AddressingMethods() []MethodSpec
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
// "smartops"). The compiler synthesizes the list from each resource's tags
// (driver.AttachAll reads this same annotation): a "<key>:<name>" tag adds
// <name>'s id to that resource's <key> list — it is never an authored key.
// Requires Resource(...) on the same node.
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
