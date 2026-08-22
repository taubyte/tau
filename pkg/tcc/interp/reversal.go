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
// That only works for a term that INVERTS. `Each("attempts")` does: the whole value lands
// in one field, and reading the field gives the value.
//
// A COMPOSED name used to not, and the reason was the string. `"{name}_{each.name}"`
// produced `checkout_reserve`, and recovering `reserve` from it meant knowing it splits at
// the first underscore — a guess the moment either half contains one, so composition was
// declared one-way and everything it read stayed in the shell.
//
// `Concat(Attr("name"), Text("_"), Each("name"))` is the same three characters of output
// and a different statement: the underscore is a separator the LOWERING wrote, and it says
// so. So the split is not a guess. The parts around the composed one are filled in — text
// is known, and so is the name of the resource being lowered, which is not config in the
// document but what the document IS — their lengths are the cut, and what is between them
// is the value, whatever it contains. Invertibility became a property the DSL states,
// which is what `.notes/v2/shell-dedup.md` asked for and could not have while a value was
// a string that had already been concatenated.

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
		// A value carried WHOLE, and one composed into something larger, are the same
		// question asked of the term: which one part of it was read off the config. A term
		// that reads one field is that part; a join has one when everything else in it is
		// known.
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
		// The NAME of the resource a lowering writes into is another: a step's `run` is
		// only ever written as the resource that carries what the step produced. Several
		// writes may go into that same resource — the queue it receives from and the one
		// it sends to are both entries on the function — and any of them gives the name
		// back.
		//
		// But only when they all name the SAME one. A lowering that writes into two
		// different resources has two names at that address and neither can be read
		// back from it.
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
