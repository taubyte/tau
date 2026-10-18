package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"unicode"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// model.go decides what TYPES a compiled object reads as, before anything is
// rendered.
//
// # Three shapes, from what the vocabulary already says
//
// A kind's fields are not one flat list, and reading them as one put a port's
// `number` and a step's `attempts` on the kind itself, where no resource has
// them. The schema already says where the seams are, so nothing here is a list:
//
//   - a NESTED GROUP (`ports`, `variables`, DefineList's `steps`) is entries,
//     and reads as map[string]<Entry> or []<Entry>;
//   - a KEY PREFIX (`regions/...`, `liveness/...`, `sql/...`) is a block, and
//     reads as a struct of its own, so a consumer asks `db.Sql.Engine` rather
//     than building `model + "/engine"`;
//   - everything else is a field of the kind.
//
// # Shared types are a finding, not a choice
//
// The vocabulary factors a block it means to be the same on every kind into a
// helper — regionAttributes(), instanceAttributes(), authorizerAttributes() —
// and that is visible here as every kind declaring IDENTICAL fields under the
// key. So the rule is structural: a block or a group whose every declaration
// agrees, on two or more owners, is ONE type named for its key (Regions,
// Instances, TransformersEntry), which a generic consumer can take whatever kind
// it came from. Where they disagree — `execution` is a call and a timeout on a
// function and a cpu on an application — each owner gets its own, named for it
// (FunctionExecution), because one type over both would carry fields a kind
// does not have.
//
// The comparison ignores what a field is VALIDATED against: `variables/at`
// accepts run and build on a function and only build on a website, and those
// are one entry with two legal sets, not two entries. Its constants are the
// union.

// typeDef is one emitted struct.
type typeDef struct {
	name string
	role string // "kind", "block" or "entry"
	key  string // the block's prefix or the group's key; the kind's own name
	list bool   // an entry of an ordered group

	members []member
	owners  []*typeDef
	base    string // the name a shared type takes
	shared  bool

	// declared is the keys whose presence says the author wrote this block, and
	// hasDeclared whether there are any. A block made only of defaulted fields
	// has none: the compiler writes it whether or not anybody did.
	declared    [][]string
	hasDeclared bool

	sig   string
	depth int
}

// member is one field of a typeDef.
type member struct {
	name string
	key  []string // from the resource the reader is handed
	loc  *Located // a scalar
	sub  *typeDef // a block or a group's entry
	note string
}

type model struct {
	kinds []string
	order []*typeDef // every distinct type, in emission order
	taken map[string]bool
}

// reservedNames is every top-level name the core file declares, so a generated
// type cannot shadow one (`Host`, `Source`, `Variable` are the core's).
func reservedNames(pkg string) map[string]bool {
	out := map[string]bool{}
	core, err := RenderCore(pkg)
	if err != nil {
		return out
	}
	f, err := parser.ParseFile(token.NewFileSet(), "core.go", core, 0)
	if err != nil {
		return out
	}
	for name := range f.Scope.Objects {
		out[name] = true
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
			out[fn.Name.Name] = true
		}
	}
	return out
}

func buildModel(root []*engine.Node, kinds []string, reserved map[string]bool) *model {
	m := &model{taken: map[string]bool{}}
	for name := range reserved {
		m.taken[name] = true
	}
	for _, name := range []string{"SourceBlock", "ReadSourceBlock", "BuildBlock", "ReadBuildBlock", "Readers", "present"} {
		m.taken[name] = true
	}

	var all []*typeDef
	var top []*typeDef
	for _, kind := range kinds {
		fields := Locations(root, kind)
		if len(fields) == 0 {
			continue
		}
		m.kinds = append(m.kinds, kind)
		t := shape("kind", kind, false, 0, fields, Groups(root, kind), &all)
		t.name = goIdent(kind)
		top = append(top, t)
	}

	// WHICH CANDIDATES ARE ONE TYPE. Grouped by role and key; one signature
	// across two or more owners is shared.
	//
	// SHALLOWEST FIRST, and only among candidates whose owner survived. Two
	// kinds declaring the same `variables` share one entry type, and the
	// `secret` block inside it is then declared once — counting the copy under
	// the second kind's entry would call one declaration shared with itself.
	canonical := map[*typeDef]*typeDef{}
	dropped := func(t *typeDef) bool {
		for o := t; o != nil && len(o.owners) > 0; o = o.owners[0] {
			if _, gone := canonical[o]; gone {
				return true
			}
		}
		return false
	}
	maxDepth := 0
	for _, t := range all {
		if t.depth > maxDepth {
			maxDepth = t.depth
		}
	}
	for depth := 1; depth <= maxDepth; depth++ {
		byKey := map[string][]*typeDef{}
		var keys []string
		for _, t := range all {
			if t.depth != depth || dropped(t) {
				continue
			}
			k := t.role + " " + t.key
			if _, ok := byKey[k]; !ok {
				keys = append(keys, k)
			}
			byKey[k] = append(byKey[k], t)
		}
		for _, k := range keys {
			group := byKey[k]
			sigs := map[string]bool{}
			for _, t := range group {
				sigs[t.sig] = true
			}
			if len(group) < 2 || len(sigs) != 1 {
				continue
			}
			first := group[0]
			first.shared = true
			for _, t := range group[1:] {
				canonical[t] = first
				mergeEnums(first, t)
			}
		}
	}
	for _, t := range append(append([]*typeDef{}, top...), all...) {
		for i := range t.members {
			if c, ok := canonical[t.members[i].sub]; ok {
				t.members[i].sub = c
			}
		}
	}

	// NAMES, owner first, so an unshared type can be named for its owner.
	var name func(t *typeDef) string
	name = func(t *typeDef) string {
		if t.name != "" {
			return t.name
		}
		base := goIdent(t.key)
		if t.role == "entry" {
			base += "Entry"
		}
		if !t.shared {
			base = name(t.owners[0]) + base
		}
		if m.taken[base] && t.role == "block" {
			base += "Block"
		}
		t.name = base
		return base
	}
	for _, t := range top {
		claim(m.taken, t.name)
	}
	var visit func(t *typeDef)
	visit = func(t *typeDef) {
		m.order = append(m.order, t)
		for _, mem := range t.members {
			if mem.sub != nil && mem.sub.name == "" {
				claim(m.taken, name(mem.sub))
				visit(mem.sub)
			}
		}
	}
	for _, t := range top {
		visit(t)
	}
	for _, t := range m.order {
		claim(m.taken, "Read"+t.name)
		for _, c := range t.constants() {
			claim(m.taken, c.name)
		}
	}
	return m
}

// claim takes a package-level name, and refuses one already taken: two things
// generated under one name is a vocabulary the naming rule cannot spell, and a
// compile error in generated code would say so less clearly.
func claim(taken map[string]bool, name string) {
	if taken[name] {
		panic(fmt.Sprintf("tcc-gen: two generated declarations are both named %s", name))
	}
	taken[name] = true
}

// shape builds the candidate type for one owner: its fields, its blocks and its
// groups' entries. Every candidate below the kind is appended to all.
func shape(role, key string, list bool, depth int, fields []Located, groups []Group, all *[]*typeDef) *typeDef {
	t := &typeDef{role: role, key: key, list: list, depth: depth}

	blocks := map[string][]Located{}
	for _, l := range fields {
		if len(l.Key) > 1 {
			blocks[l.Key[0]] = append(blocks[l.Key[0]], l)
		}
	}
	for i := range fields {
		l := fields[i]
		if len(l.Key) == 1 {
			if _, isBlock := blocks[l.Key[0]]; isBlock {
				// A SCALAR AT A BLOCK'S OWN KEY is the block written the short
				// way — `source: services/api` — and belongs in it.
				blocks[l.Key[0]] = append([]Located{l}, blocks[l.Key[0]]...)
				continue
			}
			t.members = append(t.members, member{name: goIdent(l.Path()), key: l.Key, loc: &fields[i], note: noteFor(l)})
		}
	}

	prefixes := make([]string, 0, len(blocks))
	for prefix := range blocks {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		at := blocks[prefix]
		b := &typeDef{role: "block", key: prefix, owners: []*typeDef{t}, depth: depth + 1}
		for i := range at {
			l := at[i]
			n := "Value"
			if len(l.Key) > 1 {
				n = goIdent(strings.Join(l.Key[1:], "/"))
			}
			b.members = append(b.members, member{name: n, key: l.Key, loc: &at[i], note: noteFor(l)})
			if !l.Defaulted {
				b.declared = append(b.declared, l.Key)
			}
		}
		b.hasDeclared = len(b.declared) > 0
		b.finish()
		*all = append(*all, b)
		t.members = append(t.members, member{name: goIdent(prefix), key: []string{prefix}, sub: b})
	}

	for _, g := range groups {
		e := shape("entry", g.Key, g.List, depth+1, g.Fields, g.Groups, all)
		e.owners = []*typeDef{t}
		*all = append(*all, e)
		t.members = append(t.members, member{name: goIdent(g.Key), key: []string{g.Key}, sub: e})
	}

	t.finish()
	return t
}

// finish sorts the members and computes the signature two owners compare on.
func (t *typeDef) finish() {
	sort.Slice(t.members, func(i, j int) bool { return t.members[i].name < t.members[j].name })
	for i := 1; i < len(t.members); i++ {
		if t.members[i].name == t.members[i-1].name {
			panic(fmt.Sprintf("tcc-gen: %s %q has two fields named %s", t.role, t.key, t.members[i].name))
		}
	}
	var sig strings.Builder
	fmt.Fprintf(&sig, "%s/%s/%v{", t.role, t.key, t.list)
	for _, mem := range t.members {
		fmt.Fprintf(&sig, "%s@%s:", mem.name, strings.Join(mem.key, "/"))
		if mem.loc != nil {
			fmt.Fprintf(&sig, "%s,%v,%v;", mem.loc.Read, optional(*mem.loc), mem.loc.Defaulted)
		} else {
			sig.WriteString(mem.sub.sig + ";")
		}
	}
	sig.WriteString("}")
	t.sig = sig.String()
}

// mergeEnums folds another declaration's legal values into a shared type's.
func mergeEnums(into, from *typeDef) {
	into.owners = append(into.owners, from.owners...)
	for i := range into.members {
		a, b := into.members[i].loc, from.members[i].loc
		if a == nil || b == nil {
			continue
		}
		for _, v := range b.Enum {
			if !contains(a.Enum, v) {
				a.Enum = append(a.Enum, v)
			}
		}
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// optional is a number or a flag the object may not carry, which reads as a
// pointer. A Required one is always there, and a string's absence is its empty
// value already.
func optional(l Located) bool { return !l.Required && scalarPair(l.Read) }

func scalarPair(read string) bool {
	switch read {
	case "Int", "Duration", "Float", "Bool":
		return true
	}
	return false
}

func fieldType(l Located) string {
	if optional(l) {
		return "*" + goTypeFor(l.Read)
	}
	return goTypeFor(l.Read)
}

func noteFor(l Located) string {
	var parts []string
	if l.Captures {
		parts = append(parts, "which of "+strings.Join(l.Branch, ", ")+" was written")
	} else if strings.Join(l.Key, "/") != strings.Join(l.Authored, "/") {
		parts = append(parts, "authored at "+strings.Join(l.Authored, "/"))
	}
	if l.Defaulted {
		parts = append(parts, "defaulted")
	}
	return strings.Join(parts, "; ")
}

type constant struct{ name, value, doc string }

// constants are a type's keys and its fields' legal values.
//
// THE KEYS ARE THE COMPILED PATHS, joined by "/" — the spelling a grant's
// Settings already carries a value under (`queue/max_attempts`,
// `inference/remote/endpoint`), so a consumer names the attribute rather than
// typing it. For a block they are from the resource; for an entry, from the
// entry.
func (t *typeDef) constants() []constant {
	var out []constant
	for _, mem := range t.members {
		path := strings.Join(mem.key, "/")
		out = append(out, constant{name: t.name + "Key" + mem.name, value: fmt.Sprintf("%q", path)})
	}
	for _, mem := range t.members {
		if mem.loc == nil {
			continue
		}
		for _, v := range mem.loc.Enum {
			// QUOTED BY THE FIELD'S TYPE, which is what the value has to be
			// assignable to: a list of strings carries string values too.
			value := v
			if mem.loc.Read != "Int" && mem.loc.Read != "Float" {
				value = fmt.Sprintf("%q", v)
			}
			out = append(out, constant{name: t.name + mem.name + valueIdent(v), value: value})
		}
	}
	return out
}

// valueIdent is a legal value as an identifier suffix: verify-full -> VerifyFull.
func valueIdent(v string) string {
	var out strings.Builder
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out.WriteString(strings.ToUpper(part[:1]))
		out.WriteString(part[1:])
	}
	if out.Len() == 0 {
		return "Empty"
	}
	return out.String()
}

func (m *model) renderKindNames() []byte {
	var b bytes.Buffer
	b.WriteString("// The kinds, by the name a compiled object files them under.\nconst (\n")
	for _, kind := range m.kinds {
		fmt.Fprintf(&b, "\tKind%s = %q\n", goIdent(kind), kind)
	}
	b.WriteString(")\n\n")
	return b.Bytes()
}

func (t *typeDef) render() []byte {
	var b bytes.Buffer
	switch t.role {
	case "kind":
		fmt.Fprintf(&b, "// %s is that kind, read from a compiled object.\n", t.name)
	case "block":
		fmt.Fprintf(&b, "// %s is the %q block of %s, read from a compiled object.\n", t.name, t.key, ownerNames(t))
		if !t.hasDeclared {
			fmt.Fprintf(&b, "//\n// EVERY FIELD IN IT IS DEFAULTED, so the compiler writes it whether or not the\n// author did, and the compiled object cannot say which. It has no Declared.\n")
		}
	case "entry":
		how := "keyed by name"
		if t.list {
			how = "in the order written"
		}
		fmt.Fprintf(&b, "// %s is one entry of %q in %s, %s.\n", t.name, t.key, ownerNames(t), how)
	}

	fmt.Fprintf(&b, "type %s struct {\n", t.name)
	for _, mem := range t.members {
		note := ""
		if mem.note != "" {
			note = " // " + mem.note
		}
		fmt.Fprintf(&b, "\t%s %s%s\n", mem.name, mem.goType(), note)
	}
	if t.hasDeclared {
		b.WriteString("\n\t// Declared says the author wrote this block: a field the compiler does not\n\t// default is present. A block whose fields are all defaults reads false.\n\tDeclared bool\n")
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "// Read%s decodes one from a resource.\nfunc Read%s(r Resource) %s {\n\tvar out %s\n", t.name, t.name, t.name, t.name)
	for _, mem := range t.members {
		call := callArgs(mem.key)
		switch {
		case mem.loc != nil && optional(*mem.loc):
			fmt.Fprintf(&b, "\tout.%s = present[%s](r.%s(%s))\n", mem.name, goTypeFor(mem.loc.Read), mem.loc.Read, call)
		case mem.loc != nil && scalarPair(mem.loc.Read):
			fmt.Fprintf(&b, "\tout.%s, _ = r.%s(%s)\n", mem.name, mem.loc.Read, call)
		case mem.loc != nil:
			fmt.Fprintf(&b, "\tout.%s = r.%s(%s)\n", mem.name, mem.loc.Read, call)
		case mem.sub.role == "block":
			fmt.Fprintf(&b, "\tout.%s = Read%s(r)\n", mem.name, mem.sub.name)
		case mem.sub.list:
			fmt.Fprintf(&b, "\tfor _, e := range r.List(%s) {\n\t\tout.%s = append(out.%s, Read%s(e))\n\t}\n", call, mem.name, mem.name, mem.sub.name)
		default:
			fmt.Fprintf(&b, "\tfor _, name := range r.Keys(%s) {\n\t\tif out.%s == nil {\n\t\t\tout.%s = map[string]%s{}\n\t\t}\n\t\tout.%s[name] = Read%s(r.Sub(%s, name))\n\t}\n",
				call, mem.name, mem.name, mem.sub.name, mem.name, mem.sub.name, call)
		}
	}
	if t.hasDeclared {
		var has []string
		for _, k := range t.declared {
			has = append(has, "r.Has("+callArgs(k)+")")
		}
		fmt.Fprintf(&b, "\tout.Declared = %s\n", strings.Join(has, " ||\n\t\t"))
	}
	b.WriteString("\treturn out\n}\n\n")

	consts := t.constants()
	fmt.Fprintf(&b, "// %s's keys, and the values its fields may hold.\nconst (\n", t.name)
	for _, c := range consts {
		fmt.Fprintf(&b, "\t%s = %s\n", c.name, c.value)
	}
	b.WriteString(")\n")
	return b.Bytes()
}

func (mem member) goType() string {
	if mem.loc != nil {
		return fieldType(*mem.loc)
	}
	switch {
	case mem.sub.role == "block":
		return mem.sub.name
	case mem.sub.list:
		return "[]" + mem.sub.name
	default:
		return "map[string]" + mem.sub.name
	}
}

func ownerNames(t *typeDef) string {
	var names []string
	seen := map[string]bool{}
	for _, o := range t.owners {
		if !seen[o.name] {
			seen[o.name] = true
			names = append(names, o.name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func callArgs(key []string) string {
	args := make([]string, 0, len(key))
	for _, seg := range key {
		args = append(args, fmt.Sprintf("%q", seg))
	}
	return strings.Join(args, ", ")
}
