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
		if spec, ok := resourceSpec(iter); ok {
			defs[spec] = g.objectSchema(iter, nil)
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
			defs[spec] = g.objectSchema(iter, iter.Children)
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
func resourceMap(spec, group string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Map of name -> " + spec + " (authored under " + group + "/<name>.yaml).",
		"additionalProperties": map[string]any{"$ref": "#/$defs/" + spec},
	}
}

// objectSchema projects one resource/container iterator into an object schema:
// its attributes nested by authored Path, plus (for a container) a map per nested
// resource group.
func (g jsonSchemaGen) objectSchema(iter *engine.Node, nested []*engine.Node) map[string]any {
	props := newOmap()
	var required []string
	for _, a := range iter.Attributes {
		if noStructField(a) {
			continue
		}
		leaf := g.attrSchema(a)
		segs, ok := pathSegs(a)
		if ok && !a.Key {
			setNested(props, segs, leaf)
		} else {
			// Dynamic/map-keyed location: can't be a clean nested property, so
			// surface it flat with its real path and a marker.
			leaf[g.ext+"path"] = matchPath(a)
			leaf[g.ext+"dynamic"] = true
			props.set(a.Name, leaf)
		}
		if a.Required {
			required = append(required, a.Name)
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
	out := map[string]any{"type": "object", "properties": props}
	if d, ok := iter.Meta["doc"].(string); ok && d != "" {
		out["description"] = d
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
	return out
}

// attrSchema is the leaf schema for one attribute: its base type plus every
// introspectable constraint the DSL recorded (enum/pattern/format/minimum/shape
// as standard keywords; ref/emitValidation as vendor extensions).
func (g jsonSchemaGen) attrSchema(a *engine.Attribute) map[string]any {
	s := map[string]any{}
	switch a.Type {
	case engine.TypeStringSlice:
		s["type"] = "array"
		s["items"] = map[string]any{"type": "string"}
	case engine.TypeBool:
		s["type"] = "boolean"
	case engine.TypeInt:
		s["type"] = "integer"
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
	if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok && sc.ID != "" {
		s[g.ext+"scalar"] = sc.ID // e.g. "duration" ("20s"), "bytes" ("32GB")
	}
	if e, ok := a.Meta["enum"].([]string); ok {
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
