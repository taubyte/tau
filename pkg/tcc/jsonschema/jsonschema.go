// Package jsonschema renders a tcc DSL definition as a Draft 2020-12 JSON Schema
// (structure + constraints). It imports only the generic engine, so it runs at
// runtime (including in the browser wasm build) as well as in the generator — the
// schema always matches the DSL the caller holds. It is DSL-instance agnostic:
// all Taubyte-specific labels come in through JSONSchemaOptions.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// --- DSL-introspection helpers (self-contained; read only engine types/Meta) ---

// resourceSpec is the structureSpec type name a resource iterator declares via
// Resource(...) (e.g. "Function", "SmartOp"); ok is false for non-resource groups.
func resourceSpec(iter *engine.Node) (string, bool) {
	r, ok := iter.Meta["resource"].([4]string)
	if !ok {
		return "", false
	}
	return r[2], true // [schemaPkg, iface, specType, specPkg]
}

// commonAttrs are the attributes shared by every resource group (the DSL's
// TaubyteAttributes block: id/name/description/tags), in DSL order.
func commonAttrs(root []*engine.Node) []*engine.Attribute {
	var iters []*engine.Node
	for _, g := range root {
		if len(g.Children) == 0 {
			continue
		}
		if _, ok := resourceSpec(g.Children[0]); ok {
			iters = append(iters, g.Children[0])
		}
	}
	if len(iters) == 0 {
		return nil
	}
	inAll := map[string]int{}
	for _, it := range iters {
		seen := map[string]bool{}
		for _, a := range it.Attributes {
			if !seen[a.Name] {
				seen[a.Name] = true
				inAll[a.Name]++
			}
		}
	}
	var shared []*engine.Attribute
	seen := map[string]bool{}
	for _, a := range iters[0].Attributes {
		if inAll[a.Name] == len(iters) && !seen[a.Name] {
			seen[a.Name] = true
			shared = append(shared, a)
		}
	}
	return shared
}

// pathSegs is the canonical authored location of an attribute (its Path, or its
// bare name); ok is false if any segment is a matcher (Either/All) — a dynamic,
// map-keyed location that can't be a plain nested property.
func pathSegs(a *engine.Attribute) (segs []string, ok bool) {
	if len(a.Path) == 0 {
		return []string{a.Name}, true
	}
	for _, p := range a.Path {
		s, isStr := p.(string)
		if !isStr {
			return nil, false
		}
		segs = append(segs, s)
	}
	return segs, true
}

// noStructField reports the NoStructField() annotation (the attr projects to no
// struct/wire field — folded or unimplemented), which is left out of the schema.
func noStructField(a *engine.Attribute) bool { b, _ := a.Meta["noStructField"].(bool); return b }

// JSON Schema emission. Walks the DSL and projects
// the AUTHORED config shape (nested by Path) plus every constraint the DSL
// declares as a single Draft 2020-12 document. Standard keywords carry what JSON
// Schema expresses exactly (type/enum/pattern/format/minimum/oneOf/required); the
// vendor-extension prefix (opts.ExtPrefix) carries what it cannot statically
// enforce (cross-element references, compiler-only checks like DNS). UIs ignore
// unknown keywords and agents read x-* happily, so one document serves both. The
// cross-file referential integrity these ref keys describe is enforced by the
// compiler (Compiler.Validate), not by any JSON Schema validator.
//
// The emitter is DSL-instance agnostic: it reads only the engine's generic
// annotation keys and takes all instance-specific labels (extension prefix,
// $id, title) as options. It hardcodes no resource names and has no per-resource
// switch.
//
// Fidelity note: attributes at a dynamic/map-keyed location (Either/Key paths)
// can't be a clean nested property, so they are emitted at the object root marked
// <ext>dynamic with their real <ext>path. additionalProperties is left open so
// those and any unmodeled keys still pass.

// JSONSchemaOptions carries the instance-specific labels the generic emitter
// cannot derive from the DSL: the vendor-extension keyword prefix and the
// document identity. Empty fields fall back to neutral defaults.
type JSONSchemaOptions struct {
	ExtPrefix   string // vendor keyword prefix, e.g. "x-tau-" (default "x-")
	ID          string // JSON Schema $id (omitted if empty)
	Title       string // document title (default "Configuration")
	Description string // document description (default generic)
}

// jsonSchemaGen holds the resolved extension prefix so the per-attribute helpers
// emit vendor keys without threading it through every call.
type jsonSchemaGen struct{ ext string }

// omap is an insertion-ordered string map used for `properties`, so fields appear
// in DSL declaration order (Go's map marshaling sorts keys, which JSON Schema
// consumers render as an alphabetized form — incoherent for UIs). JSON member
// order is insignificant per spec but preserved by every mainstream parser, so
// emitting in order is what keeps a generated UI's field order matching the DSL.
type omap struct {
	keys []string
	vals map[string]any
}

func newOmap() *omap { return &omap{vals: map[string]any{}} }

func (o *omap) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *omap) get(k string) (any, bool) { v, ok := o.vals[k]; return v, ok }

func (o *omap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := marshalNoHTML(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshalNoHTML(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalNoHTML marshals v with HTML escaping off, so "<name>"/">" in descriptions
// survive inside an omap's nested values (the outer encoder can't reach them).
func marshalNoHTML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// GenerateJSONSchema renders the whole DSL + constraints as a Draft 2020-12
// document. root is the generation root (resource groups + container + clouds).
func GenerateJSONSchema(root []*engine.Node, opts JSONSchemaOptions) ([]byte, error) {
	ext := opts.ExtPrefix
	if ext == "" {
		ext = "x-"
	}
	title := opts.Title
	if title == "" {
		title = "Configuration"
	}
	desc := opts.Description
	if desc == "" {
		desc = "The DSL and its constraints. " + ext + "ref / " + ext + "validation mark checks the compiler enforces that a plain JSON Schema validator cannot."
	}
	g := jsonSchemaGen{ext: ext}

	defs := map[string]any{}
	props := newOmap()
	var required []string

	// Root scalar fields (the shared id/name/description/tags block) + constraints,
	// then the resource maps — all in DSL order.
	for _, a := range commonAttrs(root) {
		props.set(a.Name, g.attrSchema(a))
		if a.Required {
			required = append(required, a.Name)
		}
	}

	for _, node := range root {
		name, _ := node.Match.(string)
		if len(node.Children) == 0 {
			continue
		}
		iter := node.Children[0]
		// A KIND NOBODY MAY WRITE IS NOT PART OF THE DOCUMENT THIS VALIDATES. This
		// schema is what an editor checks a person's config against and what a
		// completion list is built from, and a kind the compiler produces is neither
		// offered nor accepted there — it is real everywhere else (it compiles, it is
		// indexed, it decompiles), and writing one is what is not a person's to do.
		if engine.NotAuthoredOf(iter) {
			continue
		}
		if spec, ok := resourceSpec(iter); ok {
			defs[spec] = g.objectSchema(iter, nil, spec)
			props.set(name, resourceMap(spec, name))
			continue
		}
		// Container group (applications): a bare common block plus a nested map of
		// every resource kind. Keyed by its Singular() Go name.
		if iter.Group && len(iter.Children) > 0 {
			spec, _ := iter.Meta["singular"].(string)
			if spec == "" {
				return nil, fmt.Errorf("container group %q has no Singular() declaration", name)
			}
			defs[spec] = g.objectSchema(iter, iter.Children, spec)
			props.set(name, resourceMap(spec, name))
		}
		// leaf maps (clouds) decode to no type — omitted from $defs.
	}

	doc := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"title":       title,
		"description": desc,
		"type":        "object",
		"properties":  props,
		"$defs":       defs,
	}
	if opts.ID != "" {
		doc["$id"] = opts.ID
	}
	if len(required) > 0 {
		doc["required"] = required
	}

	// Encode with HTML escaping off so ">" in descriptions stays readable (the
	// artifact is documentation, not embedded in HTML). Encoder appends a newline.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// resourceMap is the "<group>: map of name -> resource" property: each authored
// file under the group dir is one entry keyed by its name.
// groupDoc is a group iterator's own documentation, if it declared any.
func groupDoc(iter *engine.Node) string {
	d, _ := iter.Meta["doc"].(string)
	return d
}

func resourceMap(spec, group string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Map of name -> " + spec + " (authored under " + group + "/<name>.yaml).",
		"additionalProperties": map[string]any{"$ref": "#/$defs/" + spec},
	}
}

// objectSchema projects one resource/container iterator into an object schema:
// its attributes nested by authored Path, plus (for a container) a map per nested
// resource group. title is the singular the DSL declares for one instance, with
// its declared casing ("SmartOp", not "Smartop") — the only place that casing
// survives, since it is otherwise just a $defs key nobody should have to parse.
func (g jsonSchemaGen) objectSchema(iter *engine.Node, nested []*engine.Node, title string) map[string]any {
	props := newOmap()
	var required []string
	for _, a := range iter.Attributes {
		if noStructField(a) {
			continue
		}
		leaf := g.attrSchema(a)
		segs, ok := pathSegs(a)
		if ok && !a.Captures {
			setNested(props, segs, leaf)
		} else {
			// Dynamic/map-keyed location: can't be a clean nested property, so
			// surface it flat with its real path and a marker.
			leaf[g.ext+"path"] = matchPath(a)
			leaf[g.ext+"dynamic"] = true
			props.set(a.Name, leaf)
		}
		if a.Required {
			switch {
			case !ok || a.Captures:
				// A dynamic (Either/Key) location is emitted as a FLAT marker
				// property, not at its real path, so naming it in `required`
				// would demand a top-level property that does not exist and
				// reject every valid document. The requirement rides the
				// marker, in the vocabulary the compat case already set.
				leaf[g.ext+"required"] = true
			case len(a.Compat) > 0:
				// A field with a legacy alias is satisfied by EITHER location,
				// and JSON Schema's `required` names exactly one. Listing it
				// would reject documents the compiler accepts (the fixtures
				// author a website's paths under source/paths), so the
				// requirement is recorded as a vendor key instead of lost.
				leaf[g.ext+"required"] = true
				if c := compatSegs(a); c != "" {
					leaf[g.ext+"required-alias"] = c
				}
			default:
				markRequired(props, &required, segs)
			}
		}
	}
	for _, node := range nested {
		name, _ := node.Match.(string)
		if len(node.Children) == 0 {
			continue
		}
		if spec, ok := resourceSpec(node.Children[0]); ok {
			props.set(name, resourceMap(spec, name))
		}
	}
	// A nested group with no Resource descriptor (a resource's access block) is data
	// belonging to this kind, so it is emitted INLINE rather than as a $def: it has
	// no type of its own and nothing else can reference it.
	for _, node := range iter.Children {
		name, _ := node.Match.(string)
		if name == "" || len(node.Children) == 0 {
			continue
		}
		if _, isResource := resourceSpec(node.Children[0]); isResource {
			continue
		}
		// A list is an ordered sequence of the same element, so it emits items
		// rather than additionalProperties — the difference the editor needs to
		// offer "add another step" instead of asking for a key.
		if node.Sequence {
			props.set(name, map[string]any{
				"type":        "array",
				"description": groupDoc(node.Children[0]),
				"items":       g.objectSchema(node.Children[0], nil, ""),
			})
			continue
		}
		group := map[string]any{
			"type":                 "object",
			"description":          groupDoc(node.Children[0]),
			"additionalProperties": g.objectSchema(node.Children[0], nil, ""),
		}
		// A KEY CAN HOLD BOTH. A kind and an attribute may be called the same thing —
		// `model` is a kind, and an agent has a `model` — so the attribute's own
		// properties sit under that key beside the group's entries. JSON Schema says
		// exactly this: `properties` names the declared ones, `additionalProperties`
		// governs every other key. Overwriting instead published a schema in which the
		// authored resource was invalid, while the compiler accepted it.
		if prev, had := props.get(name); had {
			existing, ok := prev.(map[string]any)
			if !ok {
				existing = nil
			}
			for k, v := range existing {
				if _, taken := group[k]; !taken {
					group[k] = v
				}
			}
		}
		props.set(name, group)
	}
	out := map[string]any{"type": "object", "properties": props}
	if title != "" {
		out["title"] = title // human display name for the kind (JSON Schema title)
	}
	if d, ok := iter.Meta["doc"].(string); ok && d != "" {
		out["description"] = d
	}
	if ic, ok := iter.Meta["icon"].(string); ok && ic != "" {
		out[g.ext+"icon"] = ic // semantic glyph hint; the consumer maps it to its icon set
	}
	if cat, ok := iter.Meta["category"].(string); ok && cat != "" {
		out[g.ext+"category"] = cat // what the kind is FOR; see Category() — no consumer hardcodes this
	}
	// Display sections: a presentation overlay a UI/CLI reads to render fields in
	// human sections. It rides ALONGSIDE properties (which stay faithful to the
	// authored nesting) — each field carries <ext>section, and this registry gives
	// the sections their order, titles, and descriptions. Membership is whatever
	// the fields declared, so a section can cut across the authored Path nesting.
	if secs, ok := iter.Meta["sections"].([]engine.SectionSpec); ok && len(secs) > 0 {
		list := make([]any, 0, len(secs))
		for _, s := range secs {
			entry := map[string]any{"id": s.ID, "title": s.Title}
			if s.Doc != "" {
				entry["description"] = s.Doc
			}
			if s.When != nil {
				entry["show-when"] = condition(*s.When)
			}
			list = append(list, entry)
		}
		out[g.ext+"sections"] = list
	}
	if len(required) > 0 {
		out["required"] = required
	}
	// A SWITCH IS A CLOSED CHOICE, so say so in the standard keywords. A required field
	// whose path runs through an Either — a storage's `size`, under `object` or
	// `streaming` — cannot be named in `required`, because the property it names does
	// not exist at a static path. It rode a vendor marker alone, which meant the
	// published schema accepted a storage with no size at all while the compiler
	// refused it: the two disagreed about the same document.
	//
	// `oneOf` expresses exactly what the vocabulary means — one branch, and that
	// branch's own required fields — and rides alongside the flat properties rather
	// than replacing them.
	switch cs := g.branchConstraints(iter); len(cs) {
	case 0:
	case 1:
		out["oneOf"] = cs[0]
	default:
		// ONE oneOf PER SWITCH. A proxy chooses a host (generated|zone) AND a forward
		// target, which are two independent choices — flattening both into one oneOf
		// means a valid document matches two branches and "exactly one" rejects it.
		all := make([]any, 0, len(cs))
		for _, branches := range cs {
			all = append(all, map[string]any{"oneOf": branches})
		}
		out["allOf"] = all
	}
	return out
}

// branchConstraints expands every Either switch on this kind into one alternative per
// option, carrying the fields that are required under it.
func (g jsonSchemaGen) branchConstraints(iter *engine.Node) [][]any {
	// group required attributes by the switch they sit under: the path up to the
	// Either, and the options it offers
	type switchAt struct {
		prefix  []string
		options []string
	}
	var order []string
	switches := map[string]*switchAt{}
	under := map[string]map[string][]string{} // switch key -> option -> required leaves

	for _, a := range iter.Attributes {
		if noStructField(a) || a.Captures {
			continue
		}
		at := -1
		var opts []string
		for i, seg := range a.Path {
			if fm, ok := seg.(engine.FiniteMatcher); ok {
				at, opts = i, fm.Values()
				break
			}
		}
		if at < 0 || len(opts) == 0 || at+1 >= len(a.Path) {
			continue // no switch, or the switch IS the leaf (that is the Captures attr)
		}
		// Required in EVERY branch, or required in the ones a RequiredWhen names. The
		// second is the same statement said conditionally: `engine-host` is required
		// when the engine is postgres or mysql, which is "required inside those
		// branches" and nothing else.
		want := opts
		if !a.Required {
			cond, ok := a.Meta["requiredWhen"].(engine.ConditionSpec)
			if !ok || cond.Field != capturingAttr(iter, a.Path, at) {
				continue
			}
			want = nil
			for _, o := range opts {
				if slices.Contains(cond.In, o) {
					want = append(want, o)
				}
			}
			if len(want) == 0 {
				continue
			}
		}
		var prefix, leaf []string
		for _, seg := range a.Path[:at] {
			str, ok := seg.(string)
			if !ok {
				return nil
			}
			prefix = append(prefix, str)
		}
		for _, seg := range a.Path[at+1:] {
			str, ok := seg.(string)
			if !ok {
				return nil
			}
			leaf = append(leaf, str)
		}
		// THE SWITCH'S OPTIONS ARE THE ONES IT CAPTURES, not the ones this field
		// happens to list. A database's `engine` is distributed|postgres|mysql, and
		// `engine-host` is declared over the external two alone — so grouping by the
		// field's own Either would publish a oneOf with no `distributed` branch and
		// reject a perfectly good distributed database.
		all := capturedOptions(iter, a.Path, at)
		if len(all) == 0 {
			all = opts
		}
		key := strings.Join(prefix, "/") + "\x00" + strings.Join(all, "|")
		if _, seen := switches[key]; !seen {
			switches[key] = &switchAt{prefix: prefix, options: all}
			under[key] = map[string][]string{}
			order = append(order, key)
		}
		for _, opt := range want {
			under[key][opt] = append(under[key][opt], strings.Join(leaf, "/"))
		}
	}

	var out [][]any
	for _, key := range order {
		sw := switches[key]
		var branches []any
		for _, opt := range sw.options {
			// the branch itself has to be present, and whatever is required inside it
			inner := map[string]any{"required": []string{opt}}
			if leaves := under[key][opt]; len(leaves) > 0 {
				branch := map[string]any{}
				for _, leaf := range leaves {
					markNested(branch, strings.Split(leaf, "/"))
				}
				inner["properties"] = map[string]any{opt: branch}
			}
			branches = append(branches, nest(sw.prefix, inner))
		}
		out = append(out, branches)
	}
	return out
}

// capturedOptions is the full alternative set a switch offers, read off the attribute
// that captures it.
func capturedOptions(iter *engine.Node, path []engine.StringMatch, depth int) []string {
	for _, a := range iter.Attributes {
		if !a.Captures || len(a.Path) != depth+1 || !samePrefix(a.Path, path, depth) {
			continue
		}
		if fm, ok := a.Path[depth].(engine.FiniteMatcher); ok {
			return fm.Values()
		}
	}
	return nil
}

// samePrefix reports whether two paths agree on every segment before the switch. A
// proxy has two switches at the same DEPTH — host and forward — so depth alone finds
// whichever was declared first and hands one switch the other's alternatives.
func samePrefix(a, b []engine.StringMatch, depth int) bool {
	if len(a) < depth || len(b) < depth {
		return false
	}
	for i := 0; i < depth; i++ {
		x, ok1 := a[i].(string)
		y, ok2 := b[i].(string)
		if !ok1 || !ok2 || x != y {
			return false
		}
	}
	return true
}

// capturingAttr names the Captures() attribute that decides this switch: the sibling
// whose path holds the same Either at the same depth. It is the same question the
// decompiler asks to know which branch to write into.
func capturingAttr(iter *engine.Node, path []engine.StringMatch, depth int) string {
	for _, a := range iter.Attributes {
		if !a.Captures || len(a.Path) != depth+1 || !samePrefix(a.Path, path, depth) {
			continue
		}
		if _, isEither := a.Path[depth].(engine.FiniteMatcher); isEither {
			return a.Name
		}
	}
	return ""
}

// nest wraps a constraint so it applies at a path rather than at the document root.
func nest(prefix []string, inner map[string]any) map[string]any {
	for i := len(prefix) - 1; i >= 0; i-- {
		inner = map[string]any{
			"required":   []string{prefix[i]},
			"properties": map[string]any{prefix[i]: inner},
		}
	}
	return inner
}

// markNested records that the leaf at segs is required, creating the levels above it.
func markNested(at map[string]any, segs []string) {
	if len(segs) == 1 {
		req, _ := at["required"].([]string)
		at["required"] = append(req, segs[0])
		return
	}
	props, _ := at["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		at["properties"] = props
	}
	child, _ := props[segs[0]].(map[string]any)
	if child == nil {
		child = map[string]any{}
		props[segs[0]] = child
	}
	req, _ := at["required"].([]string)
	at["required"] = append(req, segs[0])
	markNested(child, segs[1:])
}

// attrSchema is the leaf schema for one attribute: its base type plus every
// introspectable constraint the DSL recorded (enum/pattern/format/minimum/shape
// as standard keywords; ref/emitValidation as vendor extensions).
func (g jsonSchemaGen) attrSchema(a *engine.Attribute) map[string]any {
	s := map[string]any{}
	switch a.Type {
	case engine.TypeStringSlice:
		s["type"] = "array"
		items := map[string]any{"type": "string"}
		// An enum on a LIST constrains each member, not the list: putting it on the
		// array itself would say the whole array equals one of the strings.
		if e, ok := a.Meta["enum"].([]string); ok {
			items["enum"] = e
		}
		s["items"] = items
	case engine.TypeBool:
		s["type"] = "boolean"
	case engine.TypeInt:
		s["type"] = "integer"
	case engine.TypeFloat:
		s["type"] = "number"
	default: // string (incl. Duration/Bytes, authored as human strings)
		s["type"] = "string"
	}
	if l, ok := a.Meta["label"].(string); ok && l != "" {
		s["title"] = l // human display name (JSON Schema title)
	}
	if d, ok := a.Meta["doc"].(string); ok && d != "" {
		s["description"] = d
	}
	if grp, ok := a.Meta["section"].(string); ok && grp != "" {
		s[g.ext+"section"] = grp // display section id (see the object's <ext>sections)
	}
	if c, ok := a.Meta["showWhen"].(engine.ConditionSpec); ok {
		s[g.ext+"show-when"] = condition(c) // static visibility: show only when field ∈ in
	}
	if f, ok := a.Meta["requiredUnless"].(string); ok {
		// Required unless the named sibling bool is true. One datum, so a bare
		// name — the same identifier space as x-tau-required-when.field.
		s[g.ext+"required-unless"] = f
	}
	if c, ok := a.Meta["requiredWhen"].(engine.ConditionSpec); ok {
		// Conditionally required: enforced at load, so it belongs beside the
		// standard `required` list rather than in it — JSON Schema's `required`
		// is unconditional, and this depends on a sibling's value.
		s[g.ext+"required-when"] = condition(c)
	}
	if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok && sc.ID != "" {
		s[g.ext+"scalar"] = sc.ID // e.g. "duration" ("20s"), "bytes" ("32GB")
	}
	gn, generated := a.Meta["generated"].(string)
	if generated && gn != "" {
		// Minted, not authored: render read-only and fill via Session.generate.
		s[g.ext+"generated"] = gn
	}
	dv, derived := a.Meta["derived"].(string)
	if derived && dv != "" {
		// Computed, not authored: render read-only and fill via Session.derive.
		s[g.ext+"derived"] = dv
		if from, ok := a.Meta["derivedFrom"].([]string); ok && len(from) > 0 {
			s[g.ext+"derived-from"] = from
		}
	}
	if (generated && gn != "") || (derived && dv != "") {
		// Standard keyword, so generic tooling greys the field out without
		// needing to understand either vendor marker above.
		s["readOnly"] = true
	}
	if e, ok := a.Meta["enum"].([]string); ok && a.Type != engine.TypeStringSlice {
		s["enum"] = e
	}
	if p, ok := a.Meta["pattern"].(string); ok {
		s["pattern"] = p
	}
	if f, ok := a.Meta["format"].(string); ok {
		s["format"] = f
	}
	if m, ok := a.Meta["minimum"].(int); ok {
		s["minimum"] = m
	}
	if sh, ok := a.Meta["shape"].(engine.ShapeSpec); ok {
		s["oneOf"] = shapeOneOf(sh)
	}
	if r, ok := a.Meta["ref"].(engine.RefSpec); ok {
		ref := map[string]any{"group": r.Group}
		if r.Prefix != "" {
			ref["prefix"] = r.Prefix
		}
		s[g.ext+"ref"] = ref // value(s) must name a defined <group>; compiler-enforced
	}
	if v, ok := a.Meta["emitValidation"].(engine.ValidationEmit); ok {
		s[g.ext+"validation"] = v.Validator // deferred external check (e.g. "dns")
	}
	if a.Default != nil {
		s["default"] = a.Default
	}
	return s
}

// compatSegs renders an attribute's legacy alias path, or "" if it has none or
// the path is dynamic.
func compatSegs(a *engine.Attribute) string {
	out := make([]string, 0, len(a.Compat))
	for _, p := range a.Compat {
		s, ok := p.(string)
		if !ok {
			return ""
		}
		out = append(out, s)
	}
	return strings.Join(out, "/")
}

// condition renders a static visibility condition (show when Field ∈ In).
func condition(c engine.ConditionSpec) map[string]any {
	return map[string]any{"field": c.Field, "in": c.In}
}

// shapeOneOf renders a ShapeSpec as a JSON Schema oneOf of const (literals) and
// pattern (prefixes) branches.
func shapeOneOf(sh engine.ShapeSpec) []any {
	out := make([]any, 0, len(sh.Literals)+len(sh.Prefixes))
	for _, l := range sh.Literals {
		out = append(out, map[string]any{"const": l})
	}
	for _, p := range sh.Prefixes {
		out = append(out, map[string]any{"type": "string", "pattern": "^" + regexp.QuoteMeta(p)})
	}
	return out
}

// markRequired records a required field at the level it actually lives.
// JSON Schema's `required` names a property of the OBJECT IT SITS ON, so a
// nested path listed at the root ("call" for a field authored at
// execution/call) makes every valid document fail: no such top-level property
// exists. Each intermediate key becomes required on its parent too — a required
// execution/call means `execution` itself must be there to hold it.
func markRequired(props *omap, own *[]string, segs []string) {
	addOnce(own, segs[0])
	if len(segs) == 1 {
		return
	}
	node, _ := props.get(segs[0])
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	sub, ok := m["properties"].(*omap)
	if !ok {
		return
	}
	nestedReq, _ := m["required"].([]string)
	markRequired(sub, &nestedReq, segs[1:])
	m["required"] = nestedReq
}

func addOnce(list *[]string, v string) {
	for _, e := range *list {
		if e == v {
			return
		}
	}
	*list = append(*list, v)
}

// setNested writes leaf at props[seg0].properties[seg1]...[segN], creating
// intermediate ordered object schemas as needed (order = first-seen).
func setNested(props *omap, segs []string, leaf map[string]any) {
	if len(segs) == 1 {
		props.set(segs[0], leaf)
		return
	}
	var node map[string]any
	if v, ok := props.get(segs[0]); ok {
		node = v.(map[string]any)
	} else {
		node = map[string]any{"type": "object", "properties": newOmap()}
		props.set(segs[0], node)
	}
	setNested(node["properties"].(*omap), segs[1:], leaf)
}

// matchPath renders an attribute's authored Path for the dynamic-key marker,
// stringifying dynamic (Either/All) segments via their Stringer.
func matchPath(a *engine.Attribute) string {
	parts := make([]string, 0, len(a.Path))
	for _, p := range a.Path {
		switch v := p.(type) {
		case string:
			parts = append(parts, v)
		case fmt.Stringer:
			parts = append(parts, cleanMatcher(v.String()))
		default:
			parts = append(parts, "*")
		}
	}
	if len(parts) == 0 {
		return a.Name
	}
	return strings.Join(parts, "/")
}

// cleanMatcher renders a StringMatcher's String() into a friendlier dynamic-key
// form: "Either([object streaming])" -> "{object|streaming}". Relies on the same
// stable Either([...]) format the engine's dump path parses.
func cleanMatcher(s string) string {
	if inner, ok := strings.CutPrefix(s, "Either(["); ok {
		if vals, ok := strings.CutSuffix(inner, "])"); ok {
			return "{" + strings.Join(strings.Fields(vals), "|") + "}"
		}
	}
	return s
}
