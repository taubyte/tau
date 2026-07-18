package gen

import (
	"fmt"
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

// structSkip lists DSL attrs with no direct struct field: a field folded
// elsewhere (encryption-type) or unimplemented (http-methods). network-access is
// NOT skipped — it carries a StructBool annotation projecting it to Local/Public.
var structSkip = keySet(
	"functions.http-methods",
	"databases.encryption-type",
)

// StructModel is the template model for one pkg/specs/structure/<res>.go file.
type StructModel struct {
	Spec       string   // structureSpec type name, e.g. "Function"
	Fields     []Field  // DSL-derived fields (common + resource + derived + SmartOps)
	Embeds     []string // embedded interface types, e.g. "Basic", "Indexer", "Wasm"
	SpecImport string   // pkg/specs import path for the method delegates
	SpecAlias  string   // its import alias, e.g. "functionSpec"
	Methods    []string // full method source (GetName/SetId/<addressing>/GetId)
	Skipped    []string
}

// addressingMethods emits the object-addressing methods for a resource from its
// Addressing capabilities. Bodies delegate to <alias>.Tns(); the resource field
// each threads is fixed per capability (.Id for basic/index, .Name for
// wasm/index-path/name-index, .Command for services). project/app arg names are
// normalized (some hand-written variants used projectId/appId — cosmetic only).
func addressingMethods(recv, spec, alias string, caps []engine.Capability) []string {
	r := recv
	sig := func(name, args, ret string) string {
		return fmt.Sprintf("func (%s *%s) %s(%s) %s {\n", r, spec, name, args, ret)
	}
	tns := func(m, args string) string { return fmt.Sprintf("\treturn %s.Tns().%s(%s)\n}", alias, m, args) }

	out := []string{
		fmt.Sprintf("func (%s %s) GetName() string {\n\treturn %s.Name\n}", r, spec, r),
		fmt.Sprintf("func (%s *%s) SetId(id string) {\n\t%s.Id = id\n}", r, spec, r),
	}
	for _, c := range caps {
		switch c.String() {
		case "basic":
			out = append(out, sig("BasicPath", "branch, commit, project, app string", "(*common.TnsPath, error)")+tns("BasicPath", "branch, commit, project, app, "+r+".Id"))
		case "index":
			out = append(out, sig("IndexValue", "branch, project, app string", "(*common.TnsPath, error)")+tns("IndexValue", "branch, project, app, "+r+".Id"))
		case "indexPath":
			out = append(out, sig("IndexPath", "project, app string", "*common.TnsPath")+tns("IndexPath", "project, app, "+r+".Name"))
		case "http":
			out = append(out, sig("HttpPath", "fqdn string", "(*common.TnsPath, error)")+tns("HttpPath", "fqdn"))
		case "wasm":
			out = append(out, sig("WasmModulePath", "project, app string", "(*common.TnsPath, error)")+tns("WasmModulePath", "project, app, "+r+".Name"))
			out = append(out, fmt.Sprintf("func (%s *%s) ModuleName() string {\n\treturn %s.ModuleName(%s.Name)\n}", r, spec, alias, r))
		case "services":
			out = append(out, sig("ServicesPath", "project, app, serviceId string", "(*common.TnsPath, error)")+tns("ServicesPath", "project, app, serviceId, "+r+".Command"))
		case "empty":
			out = append(out, sig("EmptyPath", "branch, commit, project, app string", "(*common.TnsPath, error)")+tns("EmptyPath", "branch, commit, project, app"))
		case "websocket":
			out = append(out, sig("WebSocketHashPath", "project, app string", "(*common.TnsPath, error)")+tns("WebSocketHashPath", "project, app"))
			out = append(out, sig("WebSocketPath", "hash string", "(*common.TnsPath, error)")+tns("WebSocketPath", "hash"))
		case "nameIndex":
			out = append(out, sig("NameIndex", "", "*common.TnsPath")+tns("NameIndex", r+".Name"))
		}
	}
	out = append(out, fmt.Sprintf("func (%s *%s) GetId() string {\n\treturn %s.Id\n}", r, spec, r))
	return out
}

// Field is one struct field. Name/Type/Tag drive the Go struct emit; Required
// and Enum are extra DSL facts the TypeScript emit uses (optionality, unions)
// and the Go template ignores.
type Field struct {
	Name       string
	Type       string
	Tag        string // full tag incl. backticks, e.g. `mapstructure:"service"`, or ""
	Required   bool
	Enum       []string // permitted values (InSet); empty = none
	EnumString bool     // enum literals are strings (quote them)
}

// Structs projects each DSL resource group into a struct proposal. Unlike the
// accessor walk it includes every attribute (Key/transform/Either too), since the
// struct has a field per logical attribute regardless of how it is read/written.
func Structs(root []*engine.Node) ([]*StructModel, error) {
	var out []*StructModel
	common := attrSet(commonAttrs(root))
	for _, g := range root {
		name, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		// A group emits a struct if the DSL declares it a Resource (full: struct +
		// addressing methods + SmartOps), or if it's a nested container group — an
		// iterator that itself holds resource sub-groups (applications) — which
		// compiles to a bare struct of the common fields only, named by
		// singularizing the group (applications -> Application). Everything else
		// (leaf maps like clouds, which decode to no type) is skipped.
		var m *StructModel
		bare := false
		if d, ok := descriptorFor(iter); ok {
			alias := strings.ToLower(d.Spec) + "Spec"
			m = &StructModel{
				Spec:       d.Spec,
				Fields:     commonFields(root),
				SpecImport: "github.com/taubyte/tau/pkg/specs/" + d.SpecPkg,
				SpecAlias:  alias,
			}
			if e, ok := iter.Meta["embeds"].([]string); ok {
				m.Embeds = e
			}
			caps, _ := iter.Meta["addressing"].([]engine.Capability)
			m.Methods = addressingMethods(d.Recv, d.Spec, alias, caps)
		} else if iter.Group && len(iter.Children) > 0 {
			m = &StructModel{Spec: title(strings.TrimSuffix(name, "s")), Fields: commonFields(root)}
			bare = true
		} else {
			continue
		}
		reserved := map[string]bool{"Id": true, "Name": true, "Description": true, "Tags": true, "SmartOps": true}
		for _, a := range iter.Attributes {
			if common[a.Name] || structSkip[name+"."+a.Name] {
				continue
			}
			// A StructBool attr (network-access) projects to a bool field named
			// by the annotation, decoded from the compiled key lower(name).
			if b, ok := a.Meta["structBool"].(string); ok && b != "" {
				if reserved[b] {
					m.Skipped = append(m.Skipped, name+"."+a.Name)
					continue
				}
				reserved[b] = true
				m.Fields = append(m.Fields, Field{Name: b, Type: "bool"})
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
			f := Field{Name: nm, Type: gt, Tag: structTag(nm, a), Required: a.Required}
			if enum, ok := a.Meta["enum"].([]string); ok {
				f.Enum = enum
				_, f.EnumString = a.Meta["enumString"].(bool)
			}
			m.Fields = append(m.Fields, f)
		}
		// Derived fields and SmartOps are resource conventions — a bare container
		// struct (Application) has neither.
		if !bare {
			if db, ok := iter.Meta["derivedBools"].([]string); ok {
				for _, nm := range db {
					if reserved[nm] {
						continue
					}
					reserved[nm] = true
					m.Fields = append(m.Fields, Field{Name: nm, Type: "bool"})
				}
			}
			m.Fields = append(m.Fields, Field{Name: "SmartOps", Type: "[]string"})
		}
		out = append(out, m)
	}
	return out, nil
}

// commonFields are the struct fields every resource carries, derived from the
// DSL's shared attributes (id/name/description/tags) — name, type and the
// Required flag all read off the attribute, so the struct block never restates
// the common schema.
func commonFields(root []*engine.Node) []Field {
	var out []Field
	for _, a := range commonAttrs(root) {
		out = append(out, Field{Name: title(a.Name), Type: goType(a.Type), Required: a.Required})
	}
	return out
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
