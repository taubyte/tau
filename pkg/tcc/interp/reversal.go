package interp

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// WHAT A LOWERING GIVES BACK, and what it does not.
//
// A lowering copies values out of the resource it lowers: a step's `attempts` becomes a
// queue's `max-attempts`, a step's `run` becomes the name at the end of a link. Once the
// resources it produced are stored, those values exist twice — and two copies of a value
// are two values the moment one of them is edited, with nothing to say which one the
// author meant.
//
// So the rule is that the shell keeps the REMAINDER: the authored fields nothing it
// produced carries. Everything else is read back out of what it became.
//
// That only works for a template that INVERTS. `{each.attempts}` does: the whole value
// lands in one field, and reading the field gives the value. `{name}_{each.name}` does
// not: recovering `reserve` from `checkout_reserve` means knowing it splits at the first
// underscore, which is a guess the moment a name contains one. Composition is one-way,
// so a composed template carries nothing back and whatever it read stays in the shell.

// Carriage is one authored field a lowering carries out whole, and where it lands.
type Carriage struct {
	// Field is the authored attribute: "attempts", "run".
	Field string
	// In is the list whose elements hold it — "steps" — and empty when it is the
	// resource's own field.
	In string
	// Into is the kind produced, and As is where the value lands in it: a field name,
	// or ">op" for the name at the far end of the access declared with that op.
	Into string
	As   string
}

// linkAt addresses the name at the end of a link, which is a place a value lands just as
// much as a field is — a step's `run` is only ever written there.
const linkAt = ">"

// Carriages is every authored field of a kind that its lowerings carry out invertibly.
func Carriages(root *engine.Node, kind string) []Carriage {
	iter := iterOfKind(root, kind)
	if iter == nil {
		return nil
	}

	var out []Carriage
	for _, l := range LoweringsOf(iter) {
		add := func(tmpl, as string) {
			key, ok := soleKey(tmpl)
			if !ok {
				return
			}
			// The NEXT element's field is that element's to give back, not this one's:
			// read from here it would say a step's own `name` is whatever the step
			// before it pointed at.
			if strings.HasPrefix(key, "each.next.") {
				return
			}
			in := ""
			if rest, cut := strings.CutPrefix(key, "each."); cut {
				if l.perEach == "" {
					return
				}
				key, in = rest, l.perEach
			}
			out = append(out, Carriage{Field: key, In: in, Into: l.into, As: as})
		}

		for field, tmpl := range l.set {
			add(tmpl, field)
		}
		// The name at the far end of an access is a place a value lands, addressed by
		// the op — so two accesses sharing an op address the same place, and neither can
		// be read back from it.
		for _, r := range l.reaches {
			if !once(l.reaches, r.op) {
				continue
			}
			if r.fromKind != "" {
				add(r.fromName, linkAt+r.op)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].In != out[j].In {
			return out[i].In < out[j].In
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// Remainder is what a shell has to keep: every authored field of a kind that no lowering
// of it carries out whole, so nothing it produced can give the value back.
//
// The identity a resource is stored under — its name and id — is not here. That is not
// config the author wrote into the document, it is what the document IS.
func Remainder(root *engine.Node, kind string) []string {
	iter := iterOfKind(root, kind)
	if iter == nil {
		return nil
	}

	carried := map[string]bool{}
	for _, c := range Carriages(root, kind) {
		carried[c.In+"\x00"+c.Field] = true
	}

	idField, nameField := engine.IdentityOf(root)
	var out []string
	for _, a := range iter.Attributes {
		if a.Name == nameField || a.Name == idField || carried["\x00"+a.Name] {
			continue
		}
		out = append(out, a.Name)
	}
	// A list is authored inside the resource, so its elements' fields are the shell's
	// too — and they are where a lowering does most of its reading.
	for _, child := range iter.Children {
		list, _ := child.Match.(string)
		if list == "" || len(child.Children) == 0 {
			continue
		}
		for _, a := range child.Children[0].Attributes {
			if carried[list+"\x00"+a.Name] {
				continue
			}
			out = append(out, list+"/"+a.Name)
		}
	}
	sort.Strings(out)
	return out
}

// ReadBack recovers the authored values a lowering carried away, out of the resources it
// produced. Keyed by where the value belongs in the authored document — "attempts" for a
// field of the resource, "steps/0/attempts" for one of a list element — which is the
// same address `Source.Path` gives, so order survives a rebuild.
//
// A value that is not there is not in the map: a step with no `on-error` produced no
// failure link, and an absent value is absent rather than empty.
func ReadBack(plan []Derived, cs []Carriage) map[string]string {
	out := map[string]string{}
	for _, d := range plan {
		for _, c := range cs {
			if c.Into != d.Kind {
				continue
			}
			// Where in the authored document this came from: the element's path when it
			// came from one, the resource itself when it did not.
			at := d.Source.Path
			if (c.In == "") != (at == "") {
				continue
			}
			if c.In != "" && !strings.HasPrefix(at, c.In+"/") {
				continue
			}

			value, ok := valueAt(d, c.As)
			if !ok {
				continue
			}
			key := c.Field
			if at != "" {
				key = at + "/" + c.Field
			}
			out[key] = value
		}
	}
	return out
}

// valueAt reads the one place a carriage lands: a field of the derived resource, or the
// name at the end of one of its links.
func valueAt(d Derived, as string) (string, bool) {
	if op, ok := strings.CutPrefix(as, linkAt); ok {
		for _, g := range d.Grants {
			if g.Op == op && !g.From.Self() {
				return g.From.Name, true
			}
		}
		return "", false
	}
	v, ok := d.Fields[as]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// soleKey is the key of a template that is exactly one reference and nothing else. It is
// the static half of soleReference: whether a template CAN carry a value back, asked of
// the schema rather than of one resource's values.
func soleKey(tmpl string) (string, bool) {
	if len(tmpl) < 3 || tmpl[0] != '{' || tmpl[len(tmpl)-1] != '}' {
		return "", false
	}
	key := tmpl[1 : len(tmpl)-1]
	if strings.ContainsAny(key, "{}") {
		return "", false
	}
	return key, true
}

// once reports whether an op addresses exactly one access.
func once(rs []reach, op string) bool {
	n := 0
	for _, r := range rs {
		if r.op == op {
			n++
		}
	}
	return n == 1
}
