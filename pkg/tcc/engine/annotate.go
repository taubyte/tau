package engine

// Annotate attaches opaque metadata to an attribute, read only by code
// generators and other tooling; the engine never interprets it.
func Annotate(key string, val any) Option {
	return func(a *Attribute) {
		if a.Meta == nil {
			a.Meta = map[string]any{}
		}
		a.Meta[key] = val
	}
}

// Duration and Bytes are String attributes tagged with a ScalarSpec, read by the
// driver and tcc-gen; the engine treats them as strings. durationPattern
// mirrors time.ParseDuration's grammar, so a JSON Schema validator rejects what
// the load-time validator does; the "duration" format keyword is ISO-8601.
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

// Doc documents an attribute for schema export, as JSON Schema title and
// description; either may be "". Documentation only, and unrelated to a
// resource's description field.
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

// Icon is a semantic icon hint for a resource group ("bolt", "database"), so a
// UI picks a glyph from the DSL; which icon set draws it is the consumer's.
// Presentation only.
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

// RequiredWhen makes a field required only when a sibling attribute holds one
// of in. Unlike ShowWhen it is enforced at load like Required, and reported by
// partial validation: a field can be visible and optional.
func RequiredWhen[T string | AKey](field T, in ...string) Option {
	return Annotate("requiredWhen", ConditionSpec{Field: FieldName(field), In: in})
}

// DefaultWhen materialises a default only when the sibling discriminator holds
// one of in. For discriminated kinds: an unconditional default on a per-type
// attribute would be written into every other type's resources.
func DefaultWhen[T string | AKey](field T, in ...string) Option {
	return Annotate("defaultWhen", ConditionSpec{Field: FieldName(field), In: in})
}

// SectionSpec is a display section for a resource's fields, presentation only
// and independent of Path; membership is explicit (InSection).
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

// Capability is an opaque object-addressing tag whose meaning lives in the code
// generator; an interface so callers pass typed values.
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

// RefSpec declares that an attribute value references another resource group,
// so a driver can resolve it to that group's compiled key.
type RefSpec struct {
	Group string

	// Inbound reverses the relationship's direction: a gateway names its zone, but
	// the zone answers for the gateway.
	Inbound bool

	// Serves: traffic goes there, a handoff rather than a dependency (a route's
	// handler, a proxy's forward, a router).
	Serves bool

	// Opens: the declarer reaches nothing itself. It lets what is in its scope reach
	// the target from a region the target is not in (a tunnel).
	Opens bool

	// Namespace names the attribute on the declaring resource that holds the target's
	// namespace, for an entry of a list whose holder names it once. Empty means the
	// sibling called `namespace`.
	Namespace string

	// ByName: the value stays a name rather than resolving to the target's id, for
	// references crossing scopes the resolver does not follow or where the authored
	// word is wanted. It is still a reference.
	ByName bool

	// KindFrom names a sibling attribute holding the target's KIND, for a reference
	// whose kind is chosen in the document rather than fixed by the schema — a gateway
	// route's handler, a proxy's forward, a stream's serve. Group is empty then.
	KindFrom string
}

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

// Opens declares a reference that opens a way to its target rather than reaching
// it. See RefSpec.Opens.
func Opens() RefOpt { return func(s *RefSpec) { s.Opens = true } }

// NamespaceAt reads the target's namespace from the declaring resource's field. See
// RefSpec.Namespace.
func NamespaceAt[T string | AKey](field T) RefOpt {
	name := FieldName(field)
	return func(s *RefSpec) { s.Namespace = name }
}

// Ref declares that this attribute's value references another resource group.
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
// (domains -> EmitValidation("domain","dns")), distinct from the load-time
// Validator().
func EmitValidation(key, validator string) Option {
	return Annotate("emitValidation", ValidationEmit{Key: key, Validator: validator})
}

// WireDrop marks an attribute whose compiled wire key a driver deletes after
// consuming it.
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

// Varies lets an attribute be written per variable, as `<key>_<value>` beside
// the key. Not universal: a key identifying the thing it is on cannot vary. Each
// name must be declared on the root with Varying, checked when the engine is
// built. It appends: two Varies are two axes.
func Varies(vars ...string) Option {
	return func(a *Attribute) {
		if a.Meta == nil {
			a.Meta = map[string]any{}
		}
		was, _ := a.Meta["varies"].([]string)
		a.Meta["varies"] = append(append([]string(nil), was...), vars...)
	}
}

// Delegable marks an attribute whose value may come from the repository a
// resource's content comes from. The schema decides what may be delegated, not
// the document: identity, references and access blocks cannot be.
func Delegable() Option { return Annotate("delegable", true) }

// IsDelegable reports whether this attribute carries that mark.
func IsDelegable(a *Attribute) bool {
	if a == nil {
		return false
	}
	ok, _ := a.Meta["delegable"].(bool)
	return ok
}

// Inlined marks an attribute naming a file whose content belongs in the compiled
// object under `into`: nothing downstream of a compile can open the repository,
// so a path alone would be dead. It is a mark read by a compile pass, not a
// lowering.
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

// Varying declares the vocabulary's variables in precedence order, once on the
// root: the loader must know the set to tell an override from a typo.
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
