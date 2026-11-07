package gen

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// TypeScript emission: a typed facade over a wasm-resident editable session, one
// accessor class per resource whose async getters and setters address fields by
// (resource, field) path. Config and YAML live in wasm (pkg/tcc/wasm/session_js.go).

func tsType(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "[]string":
		return "string[]"
	case "bool":
		return "boolean"
	case "int", "uint64":
		return "number"
	default:
		return "unknown"
	}
}

// tsName lower-camelCases a Go field name, collapsing leading acronym runs
// (FQDN -> fqdn, MQTT -> mqtt, WebSocket -> webSocket, CertType -> certType).
func tsName(goField string) string {
	r := []rune(goField)
	for i := 0; i < len(r); i++ {
		if !unicode.IsUpper(r[i]) {
			break
		}
		if i > 0 && i+1 < len(r) && unicode.IsLower(r[i+1]) {
			break
		}
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// wireKey is the JSON key a compiled resource carries for a struct field: the
// lower-cased field name, which is what the compiler emits and mapstructure
// decodes case-insensitively.
func wireKey(f Field) string {
	return strings.ToLower(f.Name)
}

// tsProp quotes a property name that isn't a bare TS identifier (e.g. the
// hyphenated git/cert keys "cert-file", "repository-id").
func tsProp(k string) string {
	for _, r := range k {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$') {
			return `"` + k + `"`
		}
	}
	return k
}

// quotedEnum reports whether an enum's members are STRING literals in the union,
// read off the attribute's declared type rather than off an opt-in annotation.
// Only a number is a bare literal in TypeScript; anything else unquoted is a type
// reference, so `"GET" | ...` emitted as `GET | ...` names nine types that do not
// exist. The engine's own InSet annotates the string case, but a vocabulary that
// builds its own set-validator (EachInSet, over a StringSlice) has no reason to
// know about the annotation — and forgetting it must not produce a union of
// dangling identifiers.
func quotedEnum(a *engine.Attribute) bool {
	return a.Type != engine.TypeInt && a.Type != engine.TypeFloat
}

// tsWireType is the TS type of a compiled-shape field: its enum union if it has
// one (same alias the accessor emit declares), else the scalar mapping.
func tsWireType(spec string, f Field) string {
	if len(f.Enum) > 0 {
		return spec + upperFirst(tsName(f.Name))
	}
	return tsType(f.Type)
}

// branchArr renders a field path whose middle segment is a runtime value:
// ["source", kind] rather than [...[], kind, ...["source"]].
func branchArr(prefix []string, v string, suffix []string) string {
	parts := make([]string, 0, len(prefix)+len(suffix)+1)
	for _, p := range prefix {
		parts = append(parts, `"`+p+`"`)
	}
	parts = append(parts, v)
	for _, p := range suffix {
		parts = append(parts, `"`+p+`"`)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func tsArr(segs []string) string {
	q := make([]string, len(segs))
	for i, s := range segs {
		q[i] = `"` + s + `"`
	}
	return "[" + strings.Join(q, ", ") + "]"
}

type tsField struct {
	name string   // camelCase accessor name
	typ  string   // ts type (or enum alias)
	path []string // in-document field path
	// A branching location: the field lives under one of several alternative
	// keys (a storage's size is at object/size or streaming/size). prefix and
	// suffix are the literal segments around the choice; alts are the keys.
	// alias is the TS union naming them. isKey marks the discriminator itself,
	// whose VALUE is the chosen key rather than a stored field.
	prefix, suffix, alts []string
	alias                string // the TS union naming the alternatives
	param                string // what to call the branch argument: the
	// discriminator's own accessor name, so a
	// storage reads size(type) and not size(kind)
	isKey bool
}

// branchOf splits an attribute's authored path around its single finite matcher:
// the literal segments before it, the alternatives, and the segments after. ok
// is false for a plain path or an unbounded matcher.
func branchOf(a *engine.Attribute) (prefix, alts, suffix []string, ok bool) {
	for i, seg := range a.Path {
		switch m := seg.(type) {
		case string:
			if !ok {
				prefix = append(prefix, m)
			} else {
				suffix = append(suffix, m)
			}
		case engine.FiniteMatcher:
			if ok {
				return nil, nil, nil, false // more than one choice in one path
			}
			alts, ok = m.Values(), true
			_ = i
		case engine.StringMatcher:
			return nil, nil, nil, false // unbounded: not enumerable
		}
	}
	return prefix, alts, suffix, ok && len(alts) > 0
}

// branchSwitch is one switch's discriminator: the TS union naming its
// alternatives and what to call the argument that picks one.
type branchSwitch struct{ alias, param string }

// switchAt identifies a switch by WHERE the choice sits — the literal segments
// before it. A node's readiness/kind and liveness/kind are two switches because
// they open under two different blocks; a type's engine key and the fields that
// live under it are one because they share a prefix. NUL joins so no two
// distinct prefixes can spell the same location.
func switchAt(prefix []string) string { return strings.Join(prefix, "\x00") }

type tsResource struct {
	spec   string // e.g. "Function" -> class FunctionConfig, Session.function()
	group  string // config directory, e.g. "functions"
	fields []tsField
}

type tsEnum struct {
	name   string
	values []string
	quoted bool
}

// tsFieldsOf projects a node's attributes into TS accessor fields, registering
// each enum under an alias named for spec. Shared by resources, the container
// group and the project root, so all three get the same accessors.
func tsFieldsOf(n *engine.Node, group, spec string, enums map[string]tsEnum, enumOrder *[]string) ([]tsField, error) {
	var out []tsField
	var collisions []error
	seen := map[string]string{}
	attrs := withBlocks(n, nil)
	// A node may hold SEVERAL switches — a database's type and each type's
	// engine, a service's readiness and its liveness — so the discriminator
	// is looked up per switch, keyed by WHERE the choice sits. One alias per node
	// gave every switch the last one's name and values, which is how sqlEngine()
	// came to be typed by the admission key.
	switches := map[string]branchSwitch{}
	for _, a := range attrs {
		prefix, alts, _, ok := branchOf(a)
		if !ok || !a.Captures {
			continue
		}
		k := switchAt(prefix)
		if _, dup := switches[k]; dup {
			continue // first Key() at a location selects it
		}
		param := tsName(structFieldName(group, a))
		alias := spec + upperFirst(param)
		switches[k] = branchSwitch{alias: alias, param: param}
		if _, exists := enums[alias]; !exists {
			enums[alias] = tsEnum{name: alias, values: alts, quoted: true}
			*enumOrder = append(*enumOrder, alias)
		}
	}

	for _, a := range attrs {
		prefix, alts, suffix, branched := branchOf(a)
		if a.Captures && !branched {
			continue // a key with no alternatives is not addressable
		}
		path, ok := pathSegs(a)
		if !ok && !branched {
			continue
		}
		gt := goType(a.Type)
		if gt == "" && !a.Captures {
			continue
		}
		// NB: no scalar retype — these edit the SOURCE config, where
		// Duration/Bytes are human strings ("20s", "32GB").
		fname := tsName(structFieldName(group, a))
		// TWO ATTRIBUTES ON ONE ACCESSOR would leave one unaddressable: a block's
		// reference names its target `name`, as the resource names itself. The
		// one that comes later is named by its whole path (authorizer/name is
		// authorizerName), so nothing an earlier name meant changes, and a clash
		// even then is refused rather than dropped.
		if _, dup := seen[fname]; dup && len(path) > 1 {
			fname = tsName(joinTitled(strings.FieldsFunc(strings.Join(path, "_"), func(r rune) bool { return r == '_' })))
		}
		if first, dup := seen[fname]; dup {
			collisions = append(collisions, fmt.Errorf("%s.%s and %s.%s both come out as the accessor %q: give one an Accessor()", group, first, group, a.Name, fname))
			continue
		}
		seen[fname] = a.Name

		typ := tsType(gt)
		if vals, ok := a.Meta["enum"].([]string); ok {
			alias := spec + upperFirst(fname)
			if _, exists := enums[alias]; !exists {
				enums[alias] = tsEnum{name: alias, values: vals, quoted: quotedEnum(a)}
				*enumOrder = append(*enumOrder, alias)
			}
			typ = alias
		}
		f := tsField{name: fname, typ: typ, path: path}
		if branched {
			f.prefix, f.alts, f.suffix = prefix, alts, suffix
			sw := switches[switchAt(prefix)]
			f.alias, f.param, f.isKey = sw.alias, sw.param, a.Captures
			if f.alias == "" {
				// A branching field with no Key attribute over the same
				// alternatives has nothing to name the branch. Rather than
				// silently drop it — which is how this whole class of field
				// went unaddressable in the first place — say so.
				return nil, fmt.Errorf("%s.%s branches over %v under %v but %s declares no Key attribute there to select it",
					group, a.Name, alts, prefix, group)
			}
			if a.Captures {
				f.typ = sw.alias
			}
		}
		out = append(out, f)
	}
	if len(collisions) > 0 {
		return nil, errors.Join(collisions...)
	}
	return out, nil
}

// withBlocks is a node's own fields and its blocks', a block's addressed through its
// key the way the compiled object files it: a package's `build: {dockerfile: {path}}`
// is the package's build/dockerfile/path.
func withBlocks(n *engine.Node, at []string) []*engine.Attribute {
	out := make([]*engine.Attribute, 0, len(n.Attributes))
	for _, a := range n.Attributes {
		if len(at) == 0 {
			out = append(out, a)
			continue
		}
		c := *a
		c.Name = strings.Join(at, "/") + "/" + a.Name
		path := a.Path
		if len(path) == 0 {
			for _, seg := range strings.Split(a.Name, "/") {
				path = append(path, seg)
			}
		}
		c.Path = nil
		for _, seg := range at {
			c.Path = append(c.Path, seg)
		}
		c.Path = append(c.Path, path...)
		c.Segments = append(append([]string{}, at...), engine.CompiledSegments(a)...)
		out = append(out, &c)
	}
	for _, child := range n.Children {
		if engine.IsBlock(child) {
			key, _ := child.Match.(string)
			out = append(out, withBlocks(child, append(append([]string{}, at...), key))...)
		}
	}
	return out
}

// resourceBase is the surface EVERY resource shares, whatever its kind: its
// address, its whole document, its file, its local validation. Emitted once as a
// base class so the typed per-kind classes add only their field accessors — and
// so a caller holding a kind as a STRING (a UI rendering whatever its route
// names) gets the identical surface from Session.resource() with no typing.
const resourceBase = `/** The surface every resource has, whatever its kind. */
export class ResourceConfig {
  constructor(
    protected s: Session,
    readonly res: string[],
  ) {}

  delete(): Promise<void> {
    return this.s.binding.delete(this.s.handle, this.res);
  }
  /** Is this resource already in the config, or is it being created? */
  exists(): Promise<boolean> {
    return this.s.binding.exists(this.s.handle, this.res);
  }
  /** The whole document, as an editor holds it. */
  async doc(): Promise<Record<string, unknown>> {
    const v = await this.s.binding.get(this.s.handle, this.res, []).catch(() => null);
    return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
  }
  /** Make the document equal doc, as the minimal diff — comments on untouched
   *  lines survive, and a key missing from doc is deleted. */
  setDoc(doc: Record<string, unknown>): Promise<void> {
    return this.s.binding.setResource(this.s.handle, this.res, doc);
  }
  /** This resource's repo-relative path and exact YAML. Never assert the path
   *  yourself — where a document lives is the DSL's to decide. */
  serialize(): Promise<SerializedResource> {
    return this.s.binding.serialize(this.s.handle, this.res);
  }
  /** Where this resource's document lives, WITHOUT rendering it. Use this when
   *  you want the path per resource — serialize() reads and returns the whole
   *  file, which an inventory would then throw away. */
  location(): Promise<string> {
    return this.s.binding.location(this.s.handle, this.res);
  }
  /** Mint a DSL-declared generated value (a resource id) rather than invent one. */
  generate(field: string[]): Promise<string> {
    return this.s.binding.generate(this.s.handle, this.res, field);
  }
  /** Compile-free local validation, each issue attributed to its field.
   *  Cross-element references still need Session.validate(). */
  validate(): Promise<FieldIssue[]> {
    return this.s.binding.validateResource(this.s.handle, this.res);
  }
  validateField(field: string[], value: unknown): Promise<void> {
    return this.s.binding.validateField(this.s.handle, this.res, field, value);
  }
  /** Allowed values for a field, filtered by what the user typed. */
  complete(field: string[], partial?: string): Promise<string[]> {
    return this.s.binding.complete(this.s.handle, this.res, field, partial);
  }
}

`

// writeTSClass emits one typed accessor class: its address (built by ctor) on
// top of ResourceConfig, plus its own field accessors.
func writeTSClass(b *strings.Builder, r tsResource, ctor string) {
	fmt.Fprintf(b, "/** Typed accessors for a %s's config. */\n", strings.ToLower(r.spec))
	fmt.Fprintf(b, "export class %sConfig extends ResourceConfig {\n", r.spec)
	fmt.Fprintf(b, "  %s\n", ctor)
	for _, f := range r.fields {
		if f.isKey {
			// The discriminator's VALUE is the key its settings live under, so
			// it is read by asking which alternative the document opened —
			// there is no stored field to fetch. Switching it moves a whole
			// block, which is a document-level edit: use setDoc for that.
			fmt.Fprintf(b, "\n  /** Which %s this is — the key its settings live under. */\n", strings.ToLower(r.spec))
			fmt.Fprintf(b, "  async %s(): Promise<%s | undefined> {\n", f.name, f.typ)
			// look inside the block that HOLDS the alternatives, which is the
			// whole document only when the choice is at the top level
			fmt.Fprintf(b, "    const at = await this.s.binding.get(this.s.handle, this.res, %s).catch(() => null);\n", tsArr(f.prefix))
			fmt.Fprintf(b, "    const block = at && typeof at === \"object\" && !Array.isArray(at) ? (at as Record<string, unknown>) : {};\n")
			fmt.Fprintf(b, "    for (const k of %s as readonly string[]) if (k in block) return k as %s;\n", tsArr(f.alts), f.typ)
			fmt.Fprintf(b, "    return undefined;\n  }\n")
			continue
		}
		if f.alias != "" {
			// A branching location: the caller says which branch, because the
			// document may not have opened one yet (creating a resource) and
			// guessing would write into the wrong block.
			seg := branchArr(f.prefix, f.param, f.suffix)
			fmt.Fprintf(b, "\n  /** Lives under the %s's kind, so the branch is explicit. */\n", strings.ToLower(r.spec))
			fmt.Fprintf(b, "  async %s(%s: %s): Promise<%s | undefined> {\n", f.name, f.param, f.alias, f.typ)
			fmt.Fprintf(b, "    return (await this.s.binding.get(this.s.handle, this.res, %s)) as %s | undefined;\n  }\n", seg, f.typ)
			fmt.Fprintf(b, "  set%s(%s: %s, v: %s): Promise<void> {\n", upperFirst(f.name), f.param, f.alias, f.typ)
			fmt.Fprintf(b, "    return this.s.binding.set(this.s.handle, this.res, %s, v);\n  }\n", seg)
			fmt.Fprintf(b, "  unset%s(%s: %s): Promise<void> {\n", upperFirst(f.name), f.param, f.alias)
			fmt.Fprintf(b, "    return this.s.binding.delete(this.s.handle, this.res, %s);\n  }\n", seg)
			continue
		}
		// getter
		fmt.Fprintf(b, "\n  async %s(): Promise<%s | undefined> {\n", f.name, f.typ)
		fmt.Fprintf(b, "    return (await this.s.binding.get(this.s.handle, this.res, %s)) as %s | undefined;\n  }\n", tsArr(f.path), f.typ)
		// setter
		fmt.Fprintf(b, "  set%s(v: %s): Promise<void> {\n", upperFirst(f.name), f.typ)
		fmt.Fprintf(b, "    return this.s.binding.set(this.s.handle, this.res, %s, v);\n  }\n", tsArr(f.path))
		// unset (delete this one field)
		fmt.Fprintf(b, "  unset%s(): Promise<void> {\n", upperFirst(f.name))
		fmt.Fprintf(b, "    return this.s.binding.delete(this.s.handle, this.res, %s);\n  }\n", tsArr(f.path))
	}
	b.WriteString("}\n\n")
}

// GenerateTS renders the session accessor classes (+ Session + enum aliases).
// It takes the project ROOT node: its Children are the groups, and its own
// Attributes are the project's own fields (which Session.project() edits).
func GenerateTS(rootNode *engine.Node) ([]byte, error) {
	root := rootNode.Children
	var resources []tsResource
	var failed []error
	enums := map[string]tsEnum{}
	var enumOrder []string

	for _, g := range root {
		name, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		d, ok := descriptorFor(g.Children[0])
		if !ok {
			continue
		}
		fields, err := tsFieldsOf(g.Children[0], name, d.Spec, enums, &enumOrder)
		if err != nil {
			// EVERY RESOURCE'S, not the first's, so one run names the whole population.
			failed = append(failed, err)
			continue
		}
		resources = append(resources, tsResource{spec: d.Spec, group: name, fields: fields})
	}
	if len(failed) > 0 {
		return nil, errors.Join(failed...)
	}

	// The container group and project root are documents too and get accessor classes;
	// all field walks happen before emission so every enum alias is declared.
	container := containerKey(root)
	cSpec, err := containerSpec(root)
	if err != nil {
		return nil, err
	}
	var cFields []tsField
	if cSpec != "" {
		for _, g := range root {
			if name, _ := g.Match.(string); name == container && len(g.Children) > 0 {
				if cFields, err = tsFieldsOf(g.Children[0], container, cSpec, enums, &enumOrder); err != nil {
					return nil, err
				}
			}
		}
	}
	projectSpec, _ := rootNode.Meta["singular"].(string)
	if projectSpec == "" {
		return nil, fmt.Errorf("the schema root has no Singular() declaration")
	}
	projectFields, err2 := tsFieldsOf(rootNode, "", projectSpec, enums, &enumOrder)
	if err2 != nil {
		return nil, err2
	}
	var b strings.Builder
	b.WriteString("// Code generated by tcc-gen; DO NOT EDIT.\n")
	b.WriteString("// Typed accessors over a wasm-resident editable config session. Getters/setters\n")
	b.WriteString("// read/write fields by path across the wasm boundary; YAML lives in wasm.\n\n")
	b.WriteString(`import type {` + "\n" +
		`  SessionBinding,` + "\n" +
		`  CompileOptions,` + "\n" +
		`  CompileResult,` + "\n" +
		`  ViewsOptions,` + "\n" +
		`  ViewsResult,` + "\n" +
		`  Validation,` + "\n" +
		`  Kind,` + "\n" +
		`  FieldIssue,` + "\n" +
		`  SerializedResource,` + "\n" +
		`} from "../loader.js";` + "\n")
	b.WriteString(`import type { AsyncFs } from "../fs.js";` + "\n\n")

	for _, name := range enumOrder {
		e := enums[name]
		parts := make([]string, len(e.values))
		for i, v := range e.values {
			if e.quoted {
				parts[i] = `"` + v + `"`
			} else {
				parts[i] = v
			}
		}
		fmt.Fprintf(&b, "export type %s = %s;\n", e.name, strings.Join(parts, " | "))
	}
	if len(enumOrder) > 0 {
		b.WriteString("\n")
	}

	// Session: the editable handle, with typed resource factories.
	b.WriteString("/** An editable, wasm-resident project config session. */\n")
	b.WriteString("export class Session {\n")
	b.WriteString("  constructor(readonly binding: SessionBinding, readonly handle: number) {}\n\n")
	for _, r := range resources {
		// accessor factory (optionally application-scoped) + name lister
		fmt.Fprintf(&b, "  %s(name: string, app?: string): %sConfig {\n    return new %sConfig(this, name, app);\n  }\n", tsName(r.spec), r.spec, r.spec)
		fmt.Fprintf(&b, "  %sNames(app?: string): Promise<string[]> {\n    return this.binding.list(this.handle, app ? [%q, app, %q] : [%q]);\n  }\n", tsName(r.spec), container, r.group, r.group)
	}
	b.WriteString("\n")
	// The container group and the project root are resources too: their own
	// fields live in a document like everything else, so they get the same
	// accessor class rather than the caller hand-addressing them.
	if cSpec != "" {
		fmt.Fprintf(&b, "  %s(name: string): %sConfig {\n    return new %sConfig(this, name);\n  }\n", tsName(cSpec), cSpec, cSpec)
	}
	fmt.Fprintf(&b, "  project(): %sConfig {\n    return new %sConfig(this);\n  }\n", projectSpec, projectSpec)
	// The container's lister is <spec>Names(), like every other lister, so it cannot
	// collide with the accessor.
	if cSpec != "" {
		fmt.Fprintf(&b, "  %sNames(): Promise<string[]> {\n    return this.binding.list(this.handle, [%q]);\n  }\n", tsName(cSpec), container)
	}
	// path -> canonical address, the inverse of a resource's serialize().
	b.WriteString("  resourceAt(path: string): Promise<string[] | null> {\n    return this.binding.resourceAt(this.handle, path);\n  }\n")
	// The generic, kind-keyed entry points. Everything above is statically named
	// per kind; a consumer that only knows a kind as a string — a UI rendering
	// whatever its route names — works entirely through these, and never has to
	// rebuild an address, pluralize a kind, or special-case the container.
	b.WriteString(`  /** Every resource kind this DSL defines, with its group key and whether
   *  its instances contain resources of their own. */
  kinds(): Promise<Kind[]> {
    return this.binding.kinds(this.handle);
  }
  /** The canonical address of one resource, by kind. Accepts a kind's group key
   *  or its declared singular; unknown kinds throw rather than address nothing. */
  address(kind: string, name: string, app?: string): Promise<string[]> {
    return this.binding.address(this.handle, kind, name, app);
  }
  /** The instances of a kind in scope — the project, or one application's own. */
  names(kind: string, app?: string): Promise<string[]> {
    return this.binding.names(this.handle, kind, app);
  }
  /** Is this resource already in the config, or is it being created? */
  exists(res: string[]): Promise<boolean> {
    return this.binding.exists(this.handle, res);
  }
  /** An accessor for a resource named only by kind — the untyped sibling of the
   *  generated per-kind factories, with the identical document surface. */
  async resource(kind: string, name: string, app?: string): Promise<ResourceConfig> {
    return new ResourceConfig(this, await this.address(kind, name, app));
  }
`)
	b.WriteString("  compile(opts?: CompileOptions): Promise<CompileResult> {\n    return this.binding.compile(this.handle, opts);\n  }\n")
	b.WriteString("  validate(opts?: CompileOptions): Promise<Validation[]> {\n    return this.binding.validate(this.handle, opts);\n  }\n")
	// views() is the compile cut one view per region — the cut a deploy publishes —
	// with the canonical object beside it.
	b.WriteString("  views(opts?: ViewsOptions): Promise<ViewsResult> {\n    return this.binding.views(this.handle, opts);\n  }\n")
	b.WriteString("  save(fs: AsyncFs, dir: string): Promise<void> {\n    return this.binding.save(this.handle, fs, dir);\n  }\n")
	// fork() returns a copy-on-write child Session; edit + validate it, then
	// merge() to adopt its changes onto this one, or close() to discard.
	b.WriteString("  async fork(): Promise<Session> {\n    return new Session(this.binding, await this.binding.fork(this.handle));\n  }\n")
	b.WriteString("  merge(): Promise<void> {\n    return this.binding.merge(this.handle);\n  }\n")
	b.WriteString("  close(): Promise<void> {\n    return this.binding.close(this.handle);\n  }\n")
	b.WriteString("}\n\n")

	b.WriteString(resourceBase)

	// One accessor class per resource, plus one for the container group and one
	// for the project root — every document the DSL defines is addressed the
	// same way, so no caller has to build an address by hand.
	for _, r := range resources {
		writeTSClass(&b, r, fmt.Sprintf("constructor(s: Session, name: string, app?: string) {\n    super(s, app ? [%q, app, %q, name] : [%q, name]);\n  }", container, r.group, r.group))
	}
	if cSpec != "" {
		writeTSClass(&b, tsResource{spec: cSpec, group: container, fields: cFields},
			fmt.Sprintf("constructor(s: Session, name: string) {\n    super(s, [%q, name]);\n  }", container))
	}
	writeTSClass(&b, tsResource{spec: projectSpec, group: "", fields: projectFields},
		fmt.Sprintf("constructor(s: Session) {\n    super(s, [%q]);\n  }", engine.LeafOf(rootNode)))

	// Compiled resource shapes: the data types as decoded from the TNS object
	// (what the console receives over the wire), keyed by the compiled JSON keys.
	structs, err := Structs(root)
	if err != nil {
		return nil, err
	}
	b.WriteString("// --- Compiled resource shapes (decoded from the TNS object) ---\n\n")
	for _, m := range structs {
		if m.Bare {
			continue // the scope container is a directory, not a decode surface
		}
		fmt.Fprintf(&b, "/** %s as decoded from the compiled config object. */\n", m.Spec)
		fmt.Fprintf(&b, "export interface %s {\n", m.Spec)
		for _, f := range m.Fields {
			fmt.Fprintf(&b, "  %s?: %s;\n", tsProp(wireKey(f)), tsWireType(m.Spec, f))
		}
		b.WriteString("}\n\n")
	}

	return []byte(strings.TrimRight(b.String(), "\n") + "\n"), nil
}
