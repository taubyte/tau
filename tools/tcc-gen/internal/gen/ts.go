package gen

import (
	"fmt"
	"strings"
	"unicode"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// TypeScript emission. Reuses the same DSL walk primitives as the Go accessor
// generator (pathSegs / compatSegs / accessorName / the skip tables) and emits a
// typed facade over a wasm-resident editable session: one accessor class per
// resource whose async getters/setters read/write fields by (resource, field)
// path across the wasm boundary. The config representation and all YAML live in
// wasm (see pkg/tcc/wasm/session_js.go); this is only the typed schema.

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

// wireKey is the JSON key a compiled resource carries for a struct field — the
// mapstructure tag if declared, else the lower-cased field name (which is what
// the compiler emits and mapstructure decodes case-insensitively).
func wireKey(f Field) string {
	if f.Tag != "" {
		if i := strings.IndexByte(f.Tag, '"'); i >= 0 {
			if j := strings.IndexByte(f.Tag[i+1:], '"'); j >= 0 {
				return f.Tag[i+1 : i+1+j]
			}
		}
	}
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

// tsWireType is the TS type of a compiled-shape field: its enum union if it has
// one (same alias the accessor emit declares), else the scalar mapping.
func tsWireType(spec string, f Field) string {
	if len(f.Enum) > 0 {
		return spec + upperFirst(tsName(f.Name))
	}
	return tsType(f.Type)
}

func tsArr(segs []string) string {
	q := make([]string, len(segs))
	for i, s := range segs {
		q[i] = `"` + s + `"`
	}
	return "[" + strings.Join(q, ", ") + "]"
}

type tsField struct {
	name   string   // camelCase accessor name
	typ    string   // ts type (or enum alias)
	path   []string // in-document field path
	compat []string // legacy field path (read fallback), or nil
}

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

// repoShape is the DSL's git-repository block, read off the RepoName/RepoBranch
// annotations: `source` is the block, the provider key under it is dynamic, and
// `fullname` is the leaf that says which repository. Nothing here names a
// provider or a leaf — that is exactly the hardcoding this shape removes.
type repoShape struct{ source, fullname, branch string }

// repoShapeOf reads the shape off every group that declares one and requires
// them to agree: one generated resourceRepo serves all of them, so two kinds
// with different repo layouts would silently read the wrong key for one. Fail
// loudly instead — the generator never guesses.
func repoShapeOf(groups []*engine.Node) (*repoShape, error) {
	var found *repoShape
	for _, g := range groups {
		if len(g.Children) == 0 {
			continue
		}
		s := &repoShape{}
		for _, a := range g.Children[0].Attributes {
			p, plain := pathSegs(a)
			if b, _ := a.Meta["repoName"].(bool); b && plain && len(p) >= 2 {
				s.source, s.fullname = p[0], p[len(p)-1]
			}
			if b, _ := a.Meta["repoBranch"].(bool); b && plain && len(p) > 0 {
				s.branch = p[len(p)-1]
			}
		}
		if s.source == "" || s.fullname == "" {
			continue
		}
		if found != nil && *s != *found {
			name, _ := g.Match.(string)
			return nil, fmt.Errorf("group %q declares a repository shape %+v that disagrees with %+v", name, *s, *found)
		}
		found = s
	}
	return found, nil
}

// tsFieldsOf projects a node's attributes into TS accessor fields, registering
// each enum under an alias named for spec. Shared by resources, the container
// group and the project root, so all three get the same accessors.
func tsFieldsOf(n *engine.Node, group, spec string, enums map[string]tsEnum, enumOrder *[]string) []tsField {
	var out []tsField
	seen := map[string]bool{}
	for _, a := range n.Attributes {
		if a.Key || noStructField(a) {
			continue
		}
		path, ok := pathSegs(a)
		if !ok {
			continue
		}
		gt := goType(a.Type)
		if gt == "" {
			continue
		}
		// NB: no scalar retype — these edit the SOURCE config, where
		// Duration/Bytes are human strings ("20s", "32GB").
		fname := tsName(structFieldName(group, a))
		if seen[fname] {
			continue
		}
		seen[fname] = true

		typ := tsType(gt)
		if vals, ok := a.Meta["enum"].([]string); ok {
			alias := spec + upperFirst(fname)
			if _, exists := enums[alias]; !exists {
				_, quoted := a.Meta["enumString"].(bool)
				enums[alias] = tsEnum{name: alias, values: vals, quoted: quoted}
				*enumOrder = append(*enumOrder, alias)
			}
			typ = alias
		}
		f := tsField{name: fname, typ: typ, path: path}
		if compat, ok := compatSegs(a); ok {
			f.compat = compat
		}
		out = append(out, f)
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
		// getter
		if len(f.compat) == 0 {
			fmt.Fprintf(b, "\n  async %s(): Promise<%s | undefined> {\n", f.name, f.typ)
			fmt.Fprintf(b, "    return (await this.s.binding.get(this.s.handle, this.res, %s)) as %s | undefined;\n  }\n", tsArr(f.path), f.typ)
		} else {
			fmt.Fprintf(b, "\n  async %s(): Promise<%s | undefined> {\n", f.name, f.typ)
			fmt.Fprintf(b, "    const v = await this.s.binding.get(this.s.handle, this.res, %s);\n", tsArr(f.path))
			fmt.Fprintf(b, "    return (v ?? (await this.s.binding.get(this.s.handle, this.res, %s))) as %s | undefined;\n  }\n", tsArr(f.compat), f.typ)
		}
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
		resources = append(resources, tsResource{
			spec:   d.Spec,
			group:  name,
			fields: tsFieldsOf(g.Children[0], name, d.Spec, enums, &enumOrder),
		})
	}

	// container is the config key of the applications-style container group,
	// derived from the DSL so the app-scoping path is never a literal here;
	// cSpec is its declared Go name (applications -> Application). The container
	// and the project root are documents like any other, so they get the same
	// accessor class — all field walks happen BEFORE emission so every enum
	// alias they contribute is declared.
	container := containerKey(root)
	cSpec, err := containerSpec(root)
	if err != nil {
		return nil, err
	}
	var cFields []tsField
	if cSpec != "" {
		for _, g := range root {
			if name, _ := g.Match.(string); name == container && len(g.Children) > 0 {
				cFields = tsFieldsOf(g.Children[0], container, cSpec, enums, &enumOrder)
			}
		}
	}
	projectSpec, _ := rootNode.Meta["singular"].(string)
	if projectSpec == "" {
		return nil, fmt.Errorf("the schema root has no Singular() declaration")
	}
	projectFields := tsFieldsOf(rootNode, "", projectSpec, enums, &enumOrder)
	repo, err := repoShapeOf(root)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("// Code generated by tcc-gen; DO NOT EDIT.\n")
	b.WriteString("// Typed accessors over a wasm-resident editable config session. Getters/setters\n")
	b.WriteString("// read/write fields by path across the wasm boundary; YAML lives in wasm.\n\n")
	b.WriteString(`import type {` + "\n" +
		`  SessionBinding,` + "\n" +
		`  CompileOptions,` + "\n" +
		`  CompileResult,` + "\n" +
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

	if repo != nil {
		b.WriteString("/** The git repository backing a resource. */\n")
		b.WriteString("export interface RepoRef {\n  provider: string;\n  fullname: string;\n  branch?: string;\n}\n\n")
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
	fmt.Fprintf(&b, "  %s(): Promise<string[]> {\n    return this.binding.list(this.handle, [%q]);\n  }\n", container, container)
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
	if repo != nil {
		fmt.Fprintf(&b, `  /** The git repository backing a resource, or null if it isn't repo-backed.
   * The provider key is dynamic, so this takes whichever sub-object of %q
   * carries the repo name — no provider is named here or in the DSL walk. */
  async resourceRepo(res: string[]): Promise<RepoRef | null> {
    const src = await this.binding.get(this.handle, res, [%q]).catch(() => null);
    if (!src || typeof src !== "object" || Array.isArray(src)) return null;
    const block = src as Record<string, unknown>;
    const branch = typeof block[%q] === "string" ? (block[%q] as string) : undefined;
    for (const [provider, v] of Object.entries(block)) {
      if (!v || typeof v !== "object" || Array.isArray(v)) continue;
      const fullname = (v as Record<string, unknown>)[%q];
      if (typeof fullname === "string" && fullname) {
        return { provider, fullname, ...(branch ? { branch } : {}) };
      }
    }
    return null;
  }
`, repo.source, repo.source, repo.branch, repo.branch, repo.fullname)
	}
	b.WriteString("  compile(opts?: CompileOptions): Promise<CompileResult> {\n    return this.binding.compile(this.handle, opts);\n  }\n")
	b.WriteString("  validate(opts?: CompileOptions): Promise<Validation[]> {\n    return this.binding.validate(this.handle, opts);\n  }\n")
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
		fmt.Sprintf("constructor(s: Session) {\n    super(s, [%q]);\n  }", engine.NodeDefaultSeerLeaf))

	// Compiled resource shapes: the data types as decoded from the TNS object
	// (what the console receives over the wire), keyed by the compiled JSON keys.
	structs, err := Structs(root)
	if err != nil {
		return nil, err
	}
	b.WriteString("// --- Compiled resource shapes (decoded from the TNS object) ---\n\n")
	for _, m := range structs {
		if m.SpecImport == "" {
			continue // bare container struct (Application) — not a decode surface
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
