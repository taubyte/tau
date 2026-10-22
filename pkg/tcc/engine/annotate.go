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
// durationPattern mirrors time.ParseDuration's own grammar (a signed sequence of
// decimal numbers, each with an optional fraction and a unit — ns/us/µs/ms/s/m/h —
// or the bare "0"), so a JSON Schema validator can reject the same malformed
// strings the load-time durationValidator does, without pulling in a registered
// "duration" FORMAT keyword — that name is ISO-8601 ("P3Y6M4DT12H30M5S"), a
// different grammar entirely, and a validator that enforces known formats would
// reject every valid tau duration under that name.
const durationPattern = `^[-+]?(([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+|0)$`

func Duration[T string | AKey](name T, opts ...Option) *Attribute {
	return String(name, append(opts,
		Annotate("scalar", ScalarSpec{ID: "duration", GoType: "uint64", Parse: parseDuration, Format: formatDuration}),
		Annotate("pattern", durationPattern),
		durationValidator(),
	)...)
}

func Bytes[T string | AKey](name T, opts ...Option) *Attribute {
	return String(name, append(opts,
		Annotate("scalar", ScalarSpec{ID: "bytes", GoType: "uint64", Parse: parseBytes, Format: formatBytes}),
		bytesValidator(),
	)...)
}

// Doc documents an attribute for schema export: title is the human display name
// (how a UI/CLI titles the field, when the raw name reads poorly — id -> "ID",
// ttl -> "Time-To-Live"), desc the longer description. They become JSON Schema
// `title` and `description`. Either may be "" to omit it. Documentation-only:
// the engine and compiler ignore both. Distinct from the `description` CONFIG
// attribute some resources declare — this documents the field; that one IS a field.
func Doc(title, desc string) Option {
	return func(a *Attribute) {
		if a.Meta == nil {
			a.Meta = map[string]any{}
		}
		if title != "" {
			a.Meta["label"] = title
		}
		if desc != "" {
			a.Meta["doc"] = desc
		}
	}
}

// GroupDoc is the node-level Doc: a human-readable description for a resource or
// container group, surfaced on its schema object. Documentation-only.
func GroupDoc(text string) NodeOption {
	return GroupAnnotate("doc", text)
}

// Icon is a SEMANTIC icon hint for a resource group — "bolt", "database",
// "globe" — surfaced on its schema object so a UI/CLI picks a glyph from the
// DSL instead of keeping its own kind->icon table (three of them, in the web
// console's case). Deliberately a neutral key, not an asset path or an icon-set
// class: which library draws it stays the consumer's business, and a consumer
// that doesn't know the key just falls back to its default. Presentation-only.
func Icon(name string) NodeOption {
	return GroupAnnotate("icon", name)
}

// ConditionSpec is a simple static visibility condition: show the field or section
// only when a sibling attribute (Field) holds one of In. Presentation-only — a
// UI/CLI evaluates it; it never affects parsing or the compiled output.
type ConditionSpec struct {
	Field string
	In    []string
}

// ShowWhen makes a field's display conditional: a UI/CLI shows it only when the
// sibling attribute Field holds one of in. Schema-only, static — no compile or
// parse effect.
func ShowWhen[T string | AKey](field T, in ...string) Option {
	return Annotate("showWhen", ConditionSpec{Field: FieldName(field), In: in})
}

// RequiredWhen makes a field required only when a sibling attribute holds one of
// the given values — a function's pubsub channel matters when its trigger type
// is pubsub and is meaningless otherwise, so requiring it unconditionally would
// reject every http function. Unlike ShowWhen this is NOT presentation: it is
// enforced at load exactly like Required(), and reported by partial validation.
//
// Deliberately separate from ShowWhen. A field can be shown and optional (a
// timeout), so "required" cannot be inferred from "visible".
func RequiredWhen[T string | AKey](field T, in ...string) Option {
	return Annotate("requiredWhen", ConditionSpec{Field: FieldName(field), In: in})
}

// RequiredUnless makes a field required UNLESS a sibling boolean attribute is
// true — a database's match is a literal key and must be there, unless useRegex
// turns it into a regular expression, where empty is a valid catch-all.
//
// Deliberately not RequiredWhen with a negation. Two things differ, and the
// second is the one that bites: an ABSENT discriminator here means required (an
// unset bool is false), whereas RequiredWhen treats an absent discriminator as
// "condition cannot hold, so not required". Encoding it as a value list would
// also break silently, because the value is read as a bool on one side and
// compared as a string on the other.
// DefaultWhen scopes a default to a branch: the value is materialised only when the
// sibling discriminator holds one of these values, and the attribute is left absent
// otherwise.
//
// IT EXISTS FOR DISCRIMINATED KINDS. A kind whose `type` selects a block — `database`,
// `storage`, `sync` — has per-type attributes, and an unconditional default on one of
// them is written into every resource of every OTHER type: a log would carry a queue's
// visibility timeout, which invents a branch its author never took and makes the type
// ambiguous. See defaultHere.
func DefaultWhen[T string | AKey](field T, in ...string) Option {
	return Annotate("defaultWhen", ConditionSpec{Field: FieldName(field), In: in})
}

func RequiredUnless[T string | AKey](field T) Option {
	return Annotate("requiredUnless", FieldName(field))
}

// SectionSpec declares a human-facing section for a resource's fields — how a UI
// or CLI groups them for display. It is presentation-only (no compile effect) and
// does NOT have to align with the authored nesting: a field under one Path can sit
// in a section with a field from another. Membership is explicit (see InSection),
// never derived from Path. Title is the short heading; Doc the longer blurb.
type SectionSpec struct {
	ID    string
	Title string
	Doc   string
}

// Section declares a display section with an id, a short Title, and a longer
// description (Doc). Repeatable on a node; declaration order is display order.
// Fields join it with InSection(id). Schema-only — no compile/parse effect.
func Section(id, title, doc string) NodeOption {
	return sectionDef(SectionSpec{ID: id, Title: title, Doc: doc})
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

// InSection assigns an attribute to a display section by id (see Section), telling
// a UI/CLI which section to render the field under. Explicit and independent of
// the field's Path. Schema-only — no compile or parse effect.
func InSection(id string) Option {
	return Annotate("section", id)
}

// RepoBranch marks the attribute holding the git branch a repo-backed resource
// builds from (a sibling of the provider block). Introspection-only.
func RepoBranch() Option { return Annotate("repoBranch", true) }

// Field overrides the Go struct field name a generator emits for this attribute,
// for cases where the config-key-derived name differs from the struct field
// (e.g. github-id -> RepoID). Generation-only; no runtime effect.
func Field(goName string) Option {
	return Annotate("field", goName)
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

// Singular declares the Go type name a container group compiles to (e.g. the
// applications group -> "Application"). Required on any container group (one
// whose iterator holds resource sub-groups); the generator fails loudly rather
// than guess a singular from the plural key. Generation-only; no runtime effect.
func Singular(goName string) NodeOption {
	return GroupAnnotate("singular", goName)
}

// Indexed declares that a group takes part in the index, and at which position. The
// index accumulates buckets shared across scopes, so the ORDER groups are visited in is
// part of what comes out — and it is a fact about a vocabulary, not about the driver
// visiting them. It was a list of eight group names in the interpreter: right for one
// vocabulary and silently wrong for every other.
//
// Lower goes first. Groups that declare no position follow, in declaration order.
func Indexed(position int) NodeOption { return GroupAnnotate("indexAt", position) }

// IndexedAt reads a group's position back. The second value is false when it declares
// none, which is not an error: it means "after the ones that do".
func IndexedAt(iter *Node) (int, bool) {
	if iter == nil {
		return 0, false
	}
	at, ok := iter.Meta["indexAt"].(int)
	return at, ok
}

// RefSpec declares that an attribute value references another resource group,
// so a driver can resolve it to that group's compiled key.
type RefSpec struct {
	Group string

	// Inbound reverses the DIRECTION of the relationship. A reference is normally
	// declared by the thing that depends on the target — a function names the module it
	// is built from, and the arrow runs function -> module. Some run the other way: a
	// gateway names the ZONE it answers in, and the zone is what answers FOR the
	// gateway, so an arrow drawn from the declarer says the opposite of what happens.
	//
	// Direction is the whole claim an edge makes, so it is declared rather than guessed
	// — the same reason a verb says whether it is Delivered.
	Inbound bool

	// Serves: TRAFFIC GOES THERE. A gateway's route hands a request to its handler; a
	// proxy forwards a connection; a route with no handler hands the whole decision to
	// a router function. It used to be inferred from KindFrom being set — "the author
	// picked the kind, so it must be where traffic goes" — which is a proxy for the
	// fact and stops being true the moment a handoff has a FIXED kind, as a router
	// does. What is worth an arrow, and which arrow, is declared.
	Serves bool

	// ByName: the value stays a NAME. Most references resolve to the target's id at
	// compile; some deliberately do not — an authorizer, a workflow step's function, a
	// transformer entry — because they cross scopes the resolver does not follow, or
	// because the vocabulary wants the authored word kept.
	//
	// They are still references, and that is the point: "this field points at a
	// resource of kind K" is ONE fact, and a second term for the unresolved half would
	// mean every consumer asking two questions to learn one thing.
	ByName bool

	// KindFrom names a sibling attribute holding the target's KIND, for a reference
	// whose kind is chosen in the document rather than fixed by the schema — a gateway
	// route's handler, a proxy's forward, a stream's serve. Group is empty then.
	KindFrom string
}

// RefOpt tunes a RefSpec.
type RefOpt func(*RefSpec)

// Inbound marks a reference whose relationship runs TOWARD the resource declaring it.
// See RefSpec.Inbound.
func Inbound() RefOpt { return func(s *RefSpec) { s.Inbound = true } }

// ByName marks a reference that keeps the authored name rather than resolving to an id.
// See RefSpec.ByName.
func ByName() RefOpt { return func(s *RefSpec) { s.ByName = true } }

// KindFrom marks a reference whose target KIND is read from a sibling attribute — the
// Key() of the switch the author picked. See RefSpec.KindFrom.
func KindFrom[T string | AKey](field T) RefOpt {
	name := FieldName(field)
	return func(s *RefSpec) { s.KindFrom = name }
}

// Serves declares that traffic GOES to the referenced resource — a handoff rather than a
// dependency. See RefSpec.Serves.
func Serves() RefOpt { return func(s *RefSpec) { s.Serves = true } }

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
//   - iface:     the exported accessor interface, e.g. "Database".
//   - specType:  the structureSpec type, e.g. "Database".
//   - specPkg:   the pkg/specs/<dir> addressing-helper package.
//
// Everything else — struct name, receiver, error noun, folder constant, import
// alias, file name — derives from these. Generation-only; no runtime effect.
func Resource(schemaPkg, iface, specType, specPkg string) NodeOption {
	return GroupAnnotate("resource", [4]string{schemaPkg, iface, specType, specPkg})
}

// Varies says this attribute may be written per VARIABLE, as `<key>_<value>` beside
// the key itself. Marked rather than universal because not every field may vary: a
// key that IDENTIFIES the thing it is written on cannot, and two readings of one
// resource share it — a suffix there would make them two resources wearing one name.
// Where a field that cannot vary has to differ, the whole resource is written again,
// which is the same rule one segment up the path.
//
// Each name must be one the root declares with Varying; an attribute that varies by a
// variable the vocabulary does not have is a definition error, reported when the
// engine is built rather than at the line of the document that trips over it.
//
// APPENDS rather than sets, because Annotate overwrites and two Varies() on one
// attribute mean two axes, not the second one only.
//
// The mark has two consumers: the loader (node.go's variant-first resolution) and
// strict mode (which claims the suffixed key so it is not "unknown"). One
// declaration, and neither of them a list.
func Varies(vars ...string) Option {
	return func(a *Attribute) {
		if a.Meta == nil {
			a.Meta = map[string]any{}
		}
		was, _ := a.Meta["varies"].([]string)
		a.Meta["varies"] = append(append([]string(nil), was...), vars...)
	}
}

// VariesBy is the mark read back: the variables this attribute may be written per, in
// declaration order. nil when the attribute does not vary.
// Delegable marks an attribute whose value MAY come from the repository a
// resource's content comes from, rather than from the configuration document.
//
// IT IS THE SCHEMA'S ANSWER AND NOT THE AUTHOR'S. A resource says only WHETHER it
// delegates (`source.config`); WHAT may be delegated is this, so no document can
// widen it. Identity, references and access blocks carry no such mark and
// therefore cannot be written from somewhere else, which is what keeps the
// resource list a property of the configuration repository alone.
func Delegable() Option { return Annotate("delegable", true) }

// IsDelegable reports whether this attribute carries that mark.
func IsDelegable(a *Attribute) bool {
	if a == nil {
		return false
	}
	ok, _ := a.Meta["delegable"].(bool)
	return ok
}

// Inlined marks an attribute that names a FILE whose CONTENT belongs in the
// compiled object, under `into`.
//
// # Why the content and not the path
//
// A path is only useful to something that can open it, and nothing downstream of
// a compile can: a node holds the compiled object and an artifact store, not the
// repository. So an attribute that names a file either has its content read here
// or names something nobody will ever read -- which is what a declared-and-dead
// attribute is.
//
// # What `into` is for
//
// The content lands under a DIFFERENT key from the path, and that key is the one
// consumers already read. The author writes a file; the object carries what was in
// it; and nothing downstream learns a second shape. Which key is the VOCABULARY'S
// choice and not this package's -- engine carries the string and does not know what
// it means.
//
// IT IS NOT A LOWERING. Sets/Attr/Text/Concat compose values the object already
// holds; this reaches outside the document for one it does not, which is why it is
// a mark read by a compile pass rather than a term in that vocabulary.
func Inlined(into string) Option { return Annotate("inlined", into) }

// InlinedInto is the key this attribute's file content belongs under, or "" when
// the attribute carries no such mark.
func InlinedInto(a *Attribute) string {
	if a == nil {
		return ""
	}
	into, _ := a.Meta["inlined"].(string)
	return into
}

func VariesBy(a *Attribute) []string {
	if a == nil {
		return nil
	}
	vars, _ := a.Meta["varies"].([]string)
	return vars
}

// Varying declares the variables of this vocabulary, IN PRECEDENCE ORDER: when one
// attribute varies by several, this is the order they are tried in.
//
// Stated ONCE, on the root, like Identity, because which axes a configuration has is a
// convention of the whole vocabulary rather than of any kind in it — and because the
// loader has to know the set before it can tell an override from a typo.
func Varying(names ...string) NodeOption {
	return GroupAnnotate("variables", append([]string(nil), names...))
}

// VariablesOf reads it back. nil for a vocabulary that declares none, which is a
// vocabulary with no variant keys at all: every suffix is then a typo.
func VariablesOf(root *Node) []string {
	if root == nil {
		return nil
	}
	names, _ := root.Meta["variables"].([]string)
	return names
}
