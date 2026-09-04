// Package tcc drives the CLI's resource surface from the tcc DSL instead of
// hand-written per-resource code: the resource groups, their fields, order,
// titles, sections, enums, references and dynamic branches all come from the
// DSL's JSON Schema. Adding or changing a resource in the DSL changes the CLI
// with nothing to edit here — the same contract the web console consumes
// through the wasm build (see web-console src/tcc/descriptor.ts, which this
// mirrors).
package tcc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// --- JSON Schema subset (only what drives the CLI) ---

type condition struct {
	Field string   `json:"field"`
	In    []string `json:"in"`
}

type sectionSpec struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	ShowWhen    *condition `json:"show-when"`
}

type refSpec struct {
	Group  string `json:"group"`
	Prefix string `json:"prefix"`

	// KindFrom names the sibling field whose value says which group this
	// reference points at.
	//
	// A HANDLER BLOCK HAS NO GROUP OF ITS OWN. A gateway's route, a proxy's
	// forward and a channel's serve each name a resource whose KIND the author
	// picked one field earlier, so the target group is not knowable from the
	// schema alone — it is read out of the document being edited.
	KindFrom string `json:"kind-from"`
}

type node struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Properties  *props `json:"properties"`
	// additionalProperties is either a schema or `true`; only its $ref matters.
	AdditionalProperties json.RawMessage `json:"additionalProperties"`
	Enum                 []string        `json:"enum"`
	Pattern              string          `json:"pattern"`
	Format               string          `json:"format"`
	Section              string          `json:"x-tau-section"`
	Scalar               string          `json:"x-tau-scalar"`
	Ref                  *refSpec        `json:"x-tau-ref"`
	Validation           string          `json:"x-tau-validation"`
	Dynamic              bool            `json:"x-tau-dynamic"`
	DynPath              string          `json:"x-tau-path"`
	ShowWhen             *condition      `json:"x-tau-show-when"`
	Sections             []sectionSpec   `json:"x-tau-sections"`

	// OneOf is a switch whose branches are alternative SHAPES for one key: a
	// bare path here, or a block naming another repository. The scalar branch
	// is the key itself; each object branch's properties are fields under it.
	OneOf []*node `json:"oneOf"`

	// Required is what the schema says an author must answer, and
	// RequiredWhen is the same thing once a condition holds.
	//
	// NEITHER WAS READ BEFORE, and the CLI prompted for every field in order
	// and asked nothing twice — so a resource could be created without the one
	// value it cannot run without, and the compiler said so afterwards.
	Required     bool       `json:"x-tau-required"`
	RequiredWhen *condition `json:"x-tau-required-when"`
}

// props keeps JSON object member order — field order in the CLI is the DSL's
// authored order, which a plain map would lose.
type props struct {
	keys []string
	m    map[string]*node
}

func (p *props) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil { // '{'
		return err
	}
	p.m = map[string]*node{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key := tok.(string)
		n := &node{}
		if err := dec.Decode(n); err != nil {
			return err
		}
		p.keys = append(p.keys, key)
		p.m[key] = n
	}
	_, err := dec.Token() // '}'
	return err
}

type document struct {
	Defs       *props `json:"$defs"`
	Properties *props `json:"properties"`
}

// --- form descriptors ---

// Widget is how a field is entered and displayed. Derived from the DSL, never
// from a field name.
type Widget string

const (
	WidgetCID          Widget = "cid" // read-only content id
	WidgetText         Widget = "text"
	WidgetList         Widget = "list"
	WidgetSelect       Widget = "select"
	WidgetSwitch       Widget = "switch"
	WidgetScalar       Widget = "scalar" // human byte/duration string
	WidgetRef          Widget = "ref"    // single cross-resource reference
	WidgetRefList      Widget = "ref-list"
	WidgetBranchSelect Widget = "branch-select" // discriminator of a map-keyed subtree
)

// Condition is a resolved show-when: the field is shown only while the value at
// Path is one of In.
type Condition struct {
	Path []string
	In   []string
}

// Field is one editable attribute of a resource.
type Field struct {
	Key         string
	Path        []string // in-document path; dynamic fields resolve via WritePath
	Flag        string   // CLI flag name
	Label       string
	Description string
	Section     string
	Widget      Widget
	Enum        []string
	Scalar      string // "bytes" | "duration"
	Ref         *refSpec
	ShowWhen    *Condition

	// Dynamic (map-keyed) fields — the {a|b} syntax in x-tau-path.
	BranchPrefix []string
	Alternatives []string
	BranchSuffix []string
	IsSelector   bool

	// Required says an author must answer this. RequiredWhen says the same
	// thing once its condition holds — a field that is only required inside the
	// branch that was taken.
	Required     bool
	RequiredWhen *Condition
}

// Section groups fields for prompting and display.
type Section struct {
	ID          string
	Title       string
	Description string
	ShowWhen    *Condition
}

// Form is a resource kind's whole editable surface.
type Form struct {
	Def       string // $defs key, e.g. "Function"
	Sections  []Section
	Fields    []Field
	selectors []Field
}

// Group is a resource kind as the CLI exposes it.
type Group struct {
	Dir  string // config directory / session group, e.g. "functions"
	Name string // command name, e.g. "function"
	Def  string // $defs key, e.g. "Function"
	// Container marks a kind whose instances hold resources of their own
	// (applications). Its document lives in a directory rather than a file,
	// and it is a scope the CLI can be "inside" — see the select command.
	Container bool
}

var (
	once    sync.Once
	loaded  *document
	loadErr error
)

func load() (*document, error) {
	once.Do(func() {
		raw, err := schema.JSONSchema()
		if err != nil {
			loadErr = err
			return
		}
		doc := &document{}
		loadErr = json.Unmarshal(raw, doc)
		loaded = doc
	})
	return loaded, loadErr
}

// Groups lists the resource kinds the DSL defines: every top-level map whose
// entries are a $def.
func Groups() ([]Group, error) {
	doc, err := load()
	if err != nil {
		return nil, err
	}
	var out []Group
	for _, dir := range doc.Properties.keys {
		def := defRef(doc.Properties.m[dir].AdditionalProperties)
		if def == "" || doc.Defs.m[def] == nil {
			continue
		}
		out = append(out, Group{
			Dir:       dir,
			Name:      strings.ToLower(def),
			Def:       def,
			Container: container(doc, def),
		})
	}
	return out, nil
}

// container reports whether a kind's own properties include resource maps —
// i.e. its instances contain other resources, so each is a directory with a
// config document rather than a single file.
func container(doc *document, def string) bool {
	d := doc.Defs.m[def]
	if d == nil || d.Properties == nil {
		return false
	}
	for _, k := range d.Properties.keys {
		if r := defRef(d.Properties.m[k].AdditionalProperties); r != "" && doc.Defs.m[r] != nil {
			return true
		}
	}
	return false
}

var refRe = regexp.MustCompile(`^#/\$defs/(.+)$`)

func defRef(raw json.RawMessage) string {
	var n struct {
		Ref string `json:"$ref"`
	}
	if json.Unmarshal(raw, &n) != nil {
		return ""
	}
	m := refRe.FindStringSubmatch(n.Ref)
	if m == nil {
		return ""
	}
	return m[1]
}

type leaf struct {
	key  string
	path []string
	n    *node
}

func walk(p *props, prefix []string) []leaf {
	var out []leaf
	for _, k := range p.keys {
		n := p.m[k]
		path := append(append([]string{}, prefix...), k)

		if n.Properties != nil && !n.Dynamic {
			out = append(out, walk(n.Properties, path)...)
			continue
		}

		// A SHAPE SWITCH IS NOT A LEAF. `source` is either a path here or a
		// block naming another repository, and the branches are alternative
		// shapes for the same key — so the scalar branch IS the key and each
		// object branch's properties are fields under it.
		//
		// Without this the CLI could not see `source/git`, `source/branch` or
		// `source/path` at all: a resource's repository was unreachable from
		// the command line, and `Form.Repo()` found nothing anywhere.
		if len(n.OneOf) > 0 {
			out = append(out, oneOf(n, k, path)...)
			continue
		}

		out = append(out, leaf{key: k, path: path, n: n})
	}
	return out
}

// oneOf flattens a shape switch: the scalar branch under the key itself, every
// object branch's properties beneath it.
func oneOf(n *node, key string, path []string) []leaf {
	var out []leaf
	for _, branch := range n.OneOf {
		if branch == nil {
			continue
		}
		if branch.Properties != nil {
			out = append(out, walk(branch.Properties, path)...)
			continue
		}

		// The scalar form. It carries the switch's own documentation, so what
		// is shown for the key is what the vocabulary wrote for it.
		scalar := *branch
		if scalar.Section == "" {
			scalar.Section = n.Section
		}
		out = append(out, leaf{key: key, path: path, n: &scalar})
	}
	return out
}

// FormFor builds the form for a $defs key.
func FormFor(def string) (*Form, error) {
	doc, err := load()
	if err != nil {
		return nil, err
	}
	d := doc.Defs.m[def]
	if d == nil || d.Properties == nil {
		return nil, fmt.Errorf("no schema definition for %q", def)
	}

	leaves := walk(d.Properties, nil)
	resolve := resolver(leaves)

	f := &Form{Def: def}
	for _, l := range leaves {
		// name is the file name, not an edited field; nested resource maps
		// (an application's own resources) are handled by their own commands.
		if len(l.path) == 1 && (l.key == "name" || l.n.Type == "object") {
			continue
		}
		fd := Field{
			Key:         l.key,
			Path:        l.path,
			Label:       l.n.Title,
			Description: l.n.Description,
			Section:     l.n.Section,
			Enum:        l.n.Enum,
			Scalar:      l.n.Scalar,
			Ref:         l.n.Ref,
			Required:    l.n.Required,
		}
		if fd.Label == "" {
			fd.Label = l.key
		}
		if fd.Section == "" {
			fd.Section = "identity"
		}
		if l.n.Dynamic && l.n.DynPath != "" {
			fd.BranchPrefix, fd.Alternatives, fd.BranchSuffix, fd.IsSelector = parseDynamic(l.n.DynPath)
		}
		fd.Widget = widgetFor(l.n, fd.IsSelector)
		if l.n.ShowWhen != nil {
			fd.ShowWhen = resolve(l.n.ShowWhen)
		}
		if l.n.RequiredWhen != nil {
			fd.RequiredWhen = resolve(l.n.RequiredWhen)
		}
		f.Fields = append(f.Fields, fd)
	}
	assignFlags(f.Fields)

	for _, s := range d.Sections {
		sec := Section{ID: s.ID, Title: s.Title, Description: s.Description}
		if s.ShowWhen != nil {
			sec.ShowWhen = resolve(s.ShowWhen)
		}
		f.Sections = append(f.Sections, sec)
	}
	for _, fd := range f.Fields {
		if fd.IsSelector {
			f.selectors = append(f.selectors, fd)
		}
	}
	return f, nil
}

// parseDynamic splits an x-tau-path like "{object|streaming}/size" or
// "source/{github}" into the literal segments around the alternatives. When the
// alternatives are last, the field IS the branch selector.
func parseDynamic(tmpl string) (prefix, alts, suffix []string, selector bool) {
	segs := strings.Split(tmpl, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			return segs[:i], strings.Split(s[1:len(s)-1], "|"), segs[i+1:], i == len(segs)-1
		}
	}
	return nil, nil, nil, false
}

func widgetFor(n *node, selector bool) Widget {
	switch {
	case selector:
		return WidgetBranchSelect
	case n.Format == "cid":
		return WidgetCID
	case n.Ref != nil && n.Type == "array":
		return WidgetRefList
	case n.Ref != nil:
		return WidgetRef
	case n.Scalar != "":
		return WidgetScalar
	case len(n.Enum) > 0:
		return WidgetSelect
	case n.Type == "boolean":
		return WidgetSwitch
	case n.Type == "array":
		return WidgetList
	}
	return WidgetText
}

// assignFlags names each field's flag by its leaf key, widening the ones that
// clash by one parent segment at a time until nothing shares a flag.
//
// ONE PARENT WAS NOT ENOUGH, and the kind that proved it is `database`:
// kv/distributed/size, document/distributed/size, sql/distributed/size and
// vector/distributed/size all qualify to `distributed-size`, so three of the
// four were unreachable from the command line and the fourth silently answered
// for all of them. The widening loop cannot have that failure at any depth.
//
// A field whose whole path is exhausted keeps what it has: two fields with the
// same path are the same field, which the schema does not produce.
func assignFlags(fields []Field) {
	depth := make([]int, len(fields))
	for i := range fields {
		depth[i] = 1
		fields[i].Flag = flagAt(fields[i], 1)
	}

	for {
		sharing := map[string][]int{}
		for i := range fields {
			sharing[fields[i].Flag] = append(sharing[fields[i].Flag], i)
		}

		widened := false
		for _, indices := range sharing {
			if len(indices) < 2 {
				continue
			}
			for _, i := range indices {
				if depth[i] >= len(fields[i].Path) {
					continue // nothing left to qualify with
				}
				depth[i]++
				fields[i].Flag = flagAt(fields[i], depth[i])
				widened = true
			}
		}
		if !widened {
			return
		}
	}
}

// flagAt is a field's flag with depth-1 parent segments in front of its key.
func flagAt(f Field, depth int) string {
	parents := f.Path
	if len(parents) > 0 {
		parents = parents[:len(parents)-1]
	}
	if take := depth - 1; take < len(parents) {
		parents = parents[len(parents)-take:]
	}

	parts := make([]string, 0, len(parents)+1)
	for _, p := range parents {
		// A dynamic segment carries the raw `{a|b}` form and a path can hold a
		// slash; neither belongs in a flag name.
		parts = append(parts, flagSegment(p))
	}
	return strings.Join(append(parts, flagSegment(f.Key)), "-")
}

func flagSegment(s string) string {
	s = strings.NewReplacer("/", "-", "{", "", "}", "", "|", "-").Replace(s)
	return strings.Trim(s, "-")
}

// resolver maps a show-when's declared field id to a value path: an exact
// dashed-path match first ("certificate-type" -> certificate/type), then a
// unique leaf key ("type" -> trigger/type).
func resolver(leaves []leaf) func(*condition) *Condition {
	byJoin := map[string][]string{}
	byKey := map[string][][]string{}
	for _, l := range leaves {
		byJoin[strings.Join(l.path, "-")] = l.path
		byKey[l.key] = append(byKey[l.key], l.path)
	}
	return func(c *condition) *Condition {
		if p, ok := byJoin[c.Field]; ok {
			return &Condition{Path: p, In: c.In}
		}
		if p, ok := byKey[c.Field]; ok && len(p) == 1 {
			return &Condition{Path: p[0], In: c.In}
		}
		// Unresolved: degrade to "missing => hidden" rather than failing.
		return &Condition{Path: []string{c.Field}, In: c.In}
	}
}
