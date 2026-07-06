package gen

import (
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// Struct generation is a PARTIAL projection: the structureSpec structs carry
// things the DSL cannot express — embedded types (Wasm/Basic/Indexer), derived
// fields (e.g. Function.Secure), transform fields (network-access -> Local/Public
// bool), and hand-tuned mapstructure tags/names on git/cert fields. tcc-gen emits
// the DSL-derivable field block as a reviewable proposal; the rest is hand-merged
// at adoption. Two things ARE derived: uint64 duration/size types and the
// mapstructure tag for compat-aliased fields.

// scalarGoType maps a DSL scalar tag (engine.Duration/Bytes attach these via
// Annotate) to the Go struct field type. Pure data — no codec, no reflection.
var scalarGoType = map[string]string{
	"duration": "uint64",
	"bytes":    "uint64",
}

// structSkip lists DSL attrs with no direct struct field: value transforms whose
// struct representation is a different hand-written field (network-access ->
// Local/Public bool), a field folded elsewhere (encryption-type), or unimplemented
// (http-methods). Everything else projects to exactly one struct field.
var structSkip = keySet(
	"functions.http-methods",
	"databases.network-access", "databases.encryption-type",
	"storages.network-access",
)

// StructModel is the template model for one pkg/specs/structure/<res>.go proposal.
type StructModel struct {
	Spec    string  // structureSpec type name, e.g. "Function"
	Fields  []Field // DSL-derived fields (common + resource + SmartOps)
	Skipped []string
}

// Field is one struct field.
type Field struct {
	Name string
	Type string
	Tag  string // full tag incl. backticks, e.g. `mapstructure:"service"`, or ""
}

// Structs projects each DSL resource group into a struct proposal. Unlike the
// accessor walk it includes every attribute (Key/transform/Either too), since the
// struct has a field per logical attribute regardless of how it is read/written.
func Structs(root []*engine.Node) ([]*StructModel, error) {
	var out []*StructModel
	for _, g := range root {
		name, _ := g.Match.(string)
		d, ok := descriptors[name]
		if !ok || len(g.Children) == 0 {
			continue
		}
		m := &StructModel{Spec: d.Spec, Fields: commonFields()}
		reserved := map[string]bool{"Id": true, "Name": true, "Description": true, "Tags": true, "SmartOps": true}
		for _, a := range g.Children[0].Attributes {
			if commonAttrs[a.Name] || structSkip[name+"."+a.Name] {
				continue
			}
			gt := goType(a.Type)
			if gt == "" {
				continue
			}
			nm := structFieldName(name, a)
			if reserved[nm] {
				// name collides with a common field or a non-derivable custom
				// field (e.g. github-id -> Id / RepoID). Flag for hand-merge.
				m.Skipped = append(m.Skipped, name+"."+a.Name)
				continue
			}
			reserved[nm] = true
			if s, ok := a.Meta["scalar"].(string); ok {
				if t := scalarGoType[s]; t != "" {
					gt = t
				}
			}
			m.Fields = append(m.Fields, Field{Name: nm, Type: gt, Tag: structTag(nm, a)})
		}
		m.Fields = append(m.Fields, Field{Name: "SmartOps", Type: "[]string"})
		out = append(out, m)
	}
	return out, nil
}

func commonFields() []Field {
	return []Field{
		{Name: "Id", Type: "string"},
		{Name: "Name", Type: "string"},
		{Name: "Description", Type: "string"},
		{Name: "Tags", Type: "[]string"},
	}
}

// structFieldName is the Field("...") override if present, else the accessor name
// with hyphenated fallbacks sanitized into a valid Go identifier ("git-provider"
// -> "GitProvider").
func structFieldName(group string, a *engine.Attribute) string {
	if f, ok := a.Meta["field"].(string); ok && f != "" {
		return f
	}
	nm := accessorName(group, a)
	if !strings.Contains(nm, "-") {
		return nm
	}
	parts := strings.Split(nm, "-")
	for i := range parts {
		parts[i] = title(parts[i])
	}
	return strings.Join(parts, "")
}

// structTag derives a mapstructure tag from the compat alias when the field name
// (lower-cased) does not already match the compat key (e.g. Protocol -> "service",
// Regex -> "useRegex"). Non-compat custom tags (repository-id, cert-type) are not
// derivable and are left for hand-merge.
func structTag(fieldName string, a *engine.Attribute) string {
	if t, ok := a.Meta["tag"].(string); ok && t != "" {
		return "`mapstructure:\"" + t + "\"`"
	}
	compat, ok := compatSegs(a)
	if !ok {
		return ""
	}
	key := compat[len(compat)-1]
	if key == strings.ToLower(fieldName) {
		return ""
	}
	return "`mapstructure:\"" + key + "\"`"
}
