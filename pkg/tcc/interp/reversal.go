package interp

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// What a lowering gives back. A carried value would otherwise exist twice, so
// the shell keeps only the remainder and the rest is read back from what it
// became. That needs terms that invert: a whole value lands in one field, and a
// Concat states its separators, so a composed name is cut at their filled-in
// lengths rather than guessed.

// Carriage is one authored field a lowering carries out, and where it lands.
type Carriage struct {
	// Field is the authored attribute: "attempts", "run".
	Field string
	// In is the list whose elements hold it — "steps" — and empty when it is the
	// resource's own field.
	In string
	// Into is the kind produced, and As is where the value lands: a field name, ">" for
	// the NAME of the resource this lowering writes into, or "@" for the name of the
	// resource it produced.
	Into string
	As   string

	// Before and After are what stands on either side of the value where it landed, for
	// one a lowering COMPOSED rather than copied whole. Both are terms whose value is
	// known when a lowering is read backwards, so the cut is exact. Empty for a value
	// carried whole, which is the common case.
	Before, After Term

	// nameField is what this vocabulary calls a resource's name, which is the one field
	// of the source an anchor may read. Carried here rather than asked for again, so Cut
	// needs the schema no more than the plan does.
	nameField string
}

// Composed reports that this value was built into a larger one rather than copied.
func (c Carriage) Composed() bool { return !c.Before.Empty() || !c.After.Empty() }

// Cut takes the composed value back out of the larger one it landed in, given the name of
// the resource that was lowered. Exact: the anchors are filled in, their lengths are the
// split, and what is between them is returned untouched — so a value containing the
// separator comes back whole, which is the thing a scanner over the finished string could
// never do.
func (c Carriage) Cut(value, sourceName string) (string, bool) {
	return uncompose(value, c.Before, c.After, sourceName, c.nameField)
}

const (
	// targetName addresses the NAME of the resource a lowering wrote into, which is a
	// place a value lands just as much as a field is.
	targetName = ">"
	// producedName addresses the name of the resource the lowering PRODUCED. A step's own
	// name is only ever written there — it is what the queue is called — so that is where
	// it has to come back from.
	producedName = "@"
)

// Carriages is every authored field of a kind that its lowerings carry out invertibly.
func Carriages(root *engine.Node, kind string) []Carriage {
	iter := iterOfKind(root, kind)
	if iter == nil {
		return nil
	}
	_, nameField := engine.IdentityOf(root)

	var out []Carriage
	for _, l := range LoweringsOf(iter) {
		add := func(t Term, as string, before, after Term) {
			read, ok := t.Sole()
			if !ok {
				return
			}
			// The NEXT element's field is that element's to give back, not this one's:
			// read from here it would say a step's own `name` is whatever the step
			// before it pointed at.
			if read.Origin == OfNextElement {
				return
			}
			in := ""
			if read.Origin == OfElement {
				if l.perEach == "" {
					return
				}
				in = l.perEach
			}
			// A RESOURCE'S OWN NAME IS NOT CARRIED CONFIG. It is what the document IS,
			// not something written in it, so a shell has it either way — and Remainder
			// leaves it out for the same reason. An element's name is a different fact
			// and is carried.
			if in == "" && read.Field == nameField {
				return
			}
			out = append(out, Carriage{
				Field: read.Field, In: in, Into: l.into, As: as,
				Before: before, After: after, nameField: nameField,
			})
		}
		// Carried whole or composed, the question is the same: which one part of the term
		// was read off the config.
		carry := func(t Term, as string) {
			if part, before, after, ok := t.composed(nameField); ok {
				add(part, as, before, after)
				return
			}
			add(t, as, Term{}, Term{})
		}

		for field, t := range l.set {
			carry(t, field)
		}
		// WHAT THE PRODUCED RESOURCE IS CALLED is a place a value lands. A step's name is
		// written down exactly once, as part of the name of the queue it becomes.
		carry(l.name, producedName)
		// The name of the resource a lowering writes into is another place a value lands
		// (a step's `run`), readable only when every write names the same one.
		if t, only := soleTarget(l.writes); only {
			carry(t, targetName)
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

// Remainder is what a shell keeps: every authored field no lowering carries out
// whole. A resource's name and id are not config written in it.
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

// ReadBack recovers the values a lowering carried away from the resources it
// produced, keyed by their authored address ("attempts", "steps/0/attempts"). An
// absent value is not in the map.
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
			// A value the lowering composed into a larger one is cut back out of it, by
			// the terms that stood on either side. Exact: their filled-in lengths ARE the
			// split, so nothing is searched for and nothing is guessed.
			if c.Composed() {
				value, ok = c.Cut(value, d.Source.Name)
				if !ok {
					continue
				}
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

// valueAt reads the one place a carriage lands: a field of the derived resource, the name
// of the resource it produced, or the name at the end of one of its links.
func valueAt(d Derived, as string) (string, bool) {
	switch as {
	case producedName:
		return d.Name, d.Name != ""
	case targetName:
		for _, w := range d.Writes {
			if w.On.Kind != "" {
				return w.On.Name, true
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

// soleTarget is the name every write of a lowering goes into, when they all go into the
// same one. A TERM comparison, not a string one: two writes agree when they were declared
// the same way, which is a question about the lowering rather than about one resource's
// values.
func soleTarget(ws []write) (Term, bool) {
	var t Term
	for _, w := range ws {
		if w.onKind == "" {
			continue
		}
		if !t.Empty() && !w.onName.Equal(t) {
			return Term{}, false
		}
		t = w.onName
	}
	return t, !t.Empty()
}
