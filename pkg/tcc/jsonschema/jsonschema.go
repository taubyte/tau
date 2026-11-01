// Package jsonschema renders a tcc DSL definition as a Draft 2020-12 JSON Schema.
// It imports only the generic engine, so it runs in the browser wasm build as
// well as the generator; instance-specific labels come through
// JSONSchemaOptions.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// resourceSpec is the structureSpec type name a resource iterator declares via
// Resource(...) (e.g. "Function"); ok is false for non-resource groups.
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

// The emitter projects the authored shape (nested by Path) and every constraint.
// Standard keywords carry what JSON Schema expresses exactly; the vendor prefix
// carries what it cannot (cross-element references and compiler-only checks,
// enforced by Compiler.Validate). Attributes at a dynamic location are emitted
// at the root, marked dynamic with their real path; additionalProperties stays
// open.

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

// omap is an insertion-ordered map for `properties`, so fields appear in DSL
// order rather than alphabetized by Go's map marshaling.
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
			continue
		}
		props.set(name, g.table(node))
		if node.Required {
			required = append(required, name)
		}
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

// table is a group of named entries that are not resources: each key is one entry,
// shaped by the group's iterator. A required group must hold at least one.
func (g jsonSchemaGen) table(node *engine.Node) map[string]any {
	t := map[string]any{
		"type":                 "object",
		"description":          groupDoc(node.Children[0]),
		"additionalProperties": g.objectSchema(node.Children[0], nil, ""),
	}
	if node.Required {
		t["minProperties"] = 1
	}
	return t
}

// objectSchema projects one resource or container iterator into an object
// schema: attributes nested by Path, plus a map per nested resource group for a
// container. title is the declared singular, with its casing.
func (g jsonSchemaGen) objectSchema(iter *engine.Node, nested []*engine.Node, title string) map[string]any {
	props := newOmap()
	var required []string
	for _, a := range iter.Attributes {
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
				// A dynamic location is a flat marker property, so naming it in `required` would
				// reject every valid document; the requirement rides the marker.
				leaf[g.ext+"required"] = true
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
		group := g.table(node)
		if node.Required {
			addOnce(&required, name)
		}
		// A key can hold both an attribute's properties and a group's entries (`model`):
		// `properties` names the declared ones, `additionalProperties` the rest.
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
	// Display sections ride alongside properties: each field carries <ext>section,
	// and this registry gives their order, titles and descriptions.
	if secs, ok := iter.Meta["sections"].([]engine.SectionSpec); ok && len(secs) > 0 {
		list := make([]any, 0, len(secs))
		for _, s := range secs {
			entry := map[string]any{"id": s.ID, "title": s.Title}
			if s.Doc != "" {
				entry["description"] = s.Doc
			}
			list = append(list, entry)
		}
		out[g.ext+"sections"] = list
	}
	if len(required) > 0 {
		out["required"] = required
	}
	// A switch is a closed choice, said with `oneOf`: one branch and its own required
	// fields. A required field under an Either cannot be named in `required`, so a
	// marker alone left the schema accepting what the compiler refuses.
	switch cs := g.branchConstraints(iter); len(cs) {
	case 0:
	case 1:
		out["oneOf"] = cs[0]
	default:
		// One oneOf per switch: a proxy's host and forward are independent choices, and
		// one flattened oneOf would match two branches.
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
	type switchAt struct {
		prefix   []string
		options  []string
		optional bool   // the switch itself may be absent
		bare     string // JSON type of a VALUE at the switch's own key, "" if none
	}
	var order []string
	switches := map[string]*switchAt{}
	under := map[string]map[string][]string{} // switch key -> option -> required leaves

	// The switches are the Captures() attributes, so a switch with nothing required
	// under it (`source`) is still published as a choice.
	for _, a := range iter.Attributes {
		if !a.Captures {
			continue
		}
		at := len(a.Path) - 1
		if at < 0 {
			continue
		}
		fm, ok := a.Path[at].(engine.FiniteMatcher)
		if !ok || len(fm.Values()) == 0 {
			continue
		}
		prefix := make([]string, 0, at)
		for _, seg := range a.Path[:at] {
			str, ok := seg.(string)
			if !ok {
				prefix = nil
				break
			}
			prefix = append(prefix, str)
		}
		if prefix == nil {
			continue
		}
		key := strings.Join(prefix, "/") + "\x00" + strings.Join(fm.Values(), "|")
		if _, seen := switches[key]; seen {
			continue
		}
		switches[key] = &switchAt{
			prefix:  prefix,
			options: fm.Values(),
			// Optional unless the capturing attribute or something inside a branch is
			// required.
			optional: !a.Required && !requiredUnderBranch(iter, prefix, fm.Values()),
			bare:     bareValueType(iter, prefix),
		}
		under[key] = map[string][]string{}
		order = append(order, key)
	}

	for _, a := range iter.Attributes {
		if a.Captures {
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
		// Required in every branch, or in the branches a RequiredWhen names.
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
		// The switch's options are the ones it captures, not the ones this field lists
		// (`engine-host` covers two of `engine`'s three).
		all := capturedOptions(iter, a.Path, at)
		if len(all) == 0 {
			all = opts
		}
		key := strings.Join(prefix, "/") + "\x00" + strings.Join(all, "|")
		if _, seen := switches[key]; !seen {
			continue // no Captures() attribute owns this switch
		}
		for _, opt := range want {
			under[key][opt] = append(under[key][opt], strings.Join(leaf, "/"))
		}
	}

	var out [][]any
	for _, key := range order {
		sw := switches[key]
		var branches []any
		// A value at the switch's own key is a branch (`source: agents/support`), typed
		// so a string does not satisfy every block branch vacuously.
		if sw.bare != "" {
			branches = append(branches, nest(sw.prefix, map[string]any{"type": sw.bare}))
		}
		for _, opt := range sw.options {
			// A block branch is always an object: otherwise a scalar satisfies a
			// one-option switch's branch vacuously.
			inner := map[string]any{"type": "object", "required": []string{opt}}
			if leaves := under[key][opt]; len(leaves) > 0 {
				branch := map[string]any{}
				for _, leaf := range leaves {
					markNested(branch, strings.Split(leaf, "/"))
				}
				inner["properties"] = map[string]any{opt: branch}
			}
			branches = append(branches, nest(sw.prefix, inner))
		}
		// An optional switch is one the document may not mention.
		if sw.optional && len(sw.prefix) > 0 {
			last := len(sw.prefix) - 1
			branches = append(branches, softNest(sw.prefix[:last],
				map[string]any{"not": map[string]any{"required": []string{sw.prefix[last]}}}))
		}
		if len(branches) > 0 {
			out = append(out, branches)
		}
	}
	return out
}

// bareValueType is the JSON type of an attribute reading a VALUE at exactly this
// path — the switch's own key rather than a key beneath it — or "" when the
// vocabulary declares none. It is the same question the loader asks in
// engine.claimsBareKey, answered for the published schema.
func bareValueType(iter *engine.Node, prefix []string) string {
	if len(prefix) == 0 {
		return ""
	}
	for _, a := range iter.Attributes {
		if a.Captures || len(a.Path) != len(prefix) {
			continue
		}
		same := true
		for i, seg := range a.Path {
			str, ok := seg.(string)
			if !ok || str != prefix[i] {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		switch a.Type {
		case engine.TypeBool:
			return "boolean"
		case engine.TypeInt:
			return "integer"
		case engine.TypeFloat:
			return "number"
		case engine.TypeStringSlice:
			return "array"
		default:
			return "string"
		}
	}
	return ""
}

// softNest wraps inner in `properties` alone, with no `required` — so it says
// nothing at all about a document that omits the path, which is what an "absent"
// alternative has to do.
func softNest(prefix []string, inner map[string]any) map[string]any {
	for i := len(prefix) - 1; i >= 0; i-- {
		inner = map[string]any{"properties": map[string]any{prefix[i]: inner}}
	}
	return inner
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
	if r, ok := a.Meta["ref"].(engine.RefSpec); ok {
		ref := map[string]any{"group": r.Group}
		// A reference whose target kind is another field's value names that field, so a
		// consumer reads its value to know which group to look in.
		if r.KindFrom != "" {
			ref["kind-from"] = r.KindFrom
		}
		s[g.ext+"ref"] = ref // value(s) must name a defined <group>; compiler-enforced
	}
	if v, ok := a.Meta["emitValidation"].(engine.ValidationEmit); ok {
		s[g.ext+"validation"] = v.Validator // deferred external check (e.g. "dns")
	}
	if a.Default != nil {
		s["default"] = a.Default
		// A default scoped to a branch is filled in only when a sibling holds one
		// of these values; without the condition, the schema would claim it for
		// every document.
		if c, ok := a.Meta["defaultWhen"].(engine.ConditionSpec); ok {
			s[g.ext+"default-when"] = condition(c)
		}
	}
	return s
}

// condition renders a static visibility condition (show when Field ∈ In).
func condition(c engine.ConditionSpec) map[string]any {
	return map[string]any{"field": c.Field, "in": c.In}
}

// markRequired records a required field at the level it lives, each containing
// key required on its parent: `required` names properties of the object it sits
// on.
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
		// A key can be a value or a block (an optional switch's bare-value branch).
		if existing, ok := props.get(segs[0]); ok {
			if obj := objectMember(existing); obj != nil {
				props.set(segs[0], map[string]any{"oneOf": []any{leaf, obj}})
				return
			}
		}
		props.set(segs[0], leaf)
		return
	}
	var node map[string]any
	if v, ok := props.get(segs[0]); ok {
		if node = objectMember(v); node == nil {
			node = map[string]any{"type": "object", "properties": newOmap()}
			props.set(segs[0], map[string]any{"oneOf": []any{v, node}})
		}
	} else {
		node = map[string]any{"type": "object", "properties": newOmap()}
		props.set(segs[0], node)
	}
	setNested(node["properties"].(*omap), segs[1:], leaf)
}

// objectMember returns the object schema a key already carries: the schema itself
// when it is one, or the object branch when the key is already a value-or-block
// choice. nil when the key is only a value — which is what turns it into one.
func objectMember(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if _, has := m["properties"]; has {
		return m
	}
	if alts, ok := m["oneOf"].([]any); ok {
		for _, alt := range alts {
			if am, ok := alt.(map[string]any); ok {
				if _, has := am["properties"]; has {
					return am
				}
			}
		}
	}
	return nil
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

// requiredUnderBranch reports whether any attribute is Required() somewhere inside one
// of this switch's branches — through the Either, or through the branch key written
// literally.
func requiredUnderBranch(iter *engine.Node, prefix []string, options []string) bool {
	for _, a := range iter.Attributes {
		if a.Captures || !a.Required || len(a.Path) <= len(prefix) {
			continue
		}
		same := true
		for i, seg := range prefix {
			str, ok := a.Path[i].(string)
			if !ok || str != seg {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		switch branch := a.Path[len(prefix)].(type) {
		case string:
			if slices.Contains(options, branch) {
				return true
			}
		case engine.FiniteMatcher:
			for _, v := range branch.Values() {
				if slices.Contains(options, v) {
					return true
				}
			}
		}
	}
	return false
}
