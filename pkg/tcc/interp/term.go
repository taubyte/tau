package interp

import (
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// A term, not a template: what a lowering states about a value is a closed set
// of terms, not a string with `{}` in it. A key is segments, never a "/"; the
// plan's facts are not magic words; a Concat states its parts, so a composed
// value can be read back exactly. Terms are data with unexported fields, so they
// print and every consumer can exhaust them.

// Origin is where one term's value comes from. Those reading config reach the
// resource, the element and the next element, no further. The produced kind and
// name are not terms: AtProduced says so structurally.
type Origin uint8

const (
	// OfText is fixed text, stated by the lowering itself.
	OfText Origin = iota
	// OfSource is a field of the resource being lowered.
	OfSource
	// OfElement is a field of the current element of the list PerEach names.
	OfElement
	// OfNextElement is a field of the element after that one.
	OfNextElement
	// OfScope is where this lowering put what it produced, absolute from the root:
	// an entry on somebody else's resource must say where the thing it names lives.
	OfScope
	// OfJoin is several terms, in order, one after the other.
	OfJoin
)

func (o Origin) String() string {
	switch o {
	case OfText:
		return "text"
	case OfSource:
		return "a field of what is lowered"
	case OfElement:
		return "a field of the element"
	case OfNextElement:
		return "a field of the next element"
	case OfScope:
		return "where what is produced was put"
	case OfJoin:
		return "a join"
	}
	return "unknown"
}

// reads reports whether this origin takes its value out of the config being lowered, as
// opposed to out of the lowering itself or out of the plan.
func (o Origin) reads() bool {
	return o == OfSource || o == OfElement || o == OfNextElement
}

// Term is one value a lowering states, built with Text, Attr, Each, Next, Scope
// or Concat; the zero Term says nothing.
type Term struct {
	origin Origin
	// key is the attribute read, in segments; the engine joins them only to print.
	key   []string // OfSource, OfElement, OfNextElement
	text  string   // OfText
	parts []Term   // OfJoin
}

// Text is fixed text: a separator, a suffix, a value that is the same for every resource
// this lowering runs over.
func Text(s string) Term { return Term{origin: OfText, text: s} }

// Attr is a field of the resource being lowered: a plain key, or
// Key(...) segments for a nested one.
func Attr[T string | engine.AKey](field T) Term {
	return Term{origin: OfSource, key: segments(field)}
}

// Each is a field of the current element of the list PerEach names.
func Each[T string | engine.AKey](field T) Term {
	return Term{origin: OfElement, key: segments(field)}
}

// Next is a field of the element after the current one, one step only: a chain
// is why a list is ordered, and further is a query language.
func Next[T string | engine.AKey](field T) Term {
	return Term{origin: OfNextElement, key: segments(field)}
}

// Scope is where this lowering put what it produced, absolute from the root;
// empty for a vocabulary with no Lowerable, which drops any write using it.
func Scope() Term { return Term{origin: OfScope} }

// segments is an attribute's key as levels of the object, from either spelling.
func segments[T string | engine.AKey](field T) []string {
	switch f := any(field).(type) {
	case engine.AKey:
		return append([]string{}, f...)
	case string:
		if f == "" {
			return nil
		}
		return []string{f}
	}
	return nil
}

// Concat is several terms in order. Unlike a template it still knows which
// separators it wrote, which reversal.go needs. Nested joins flatten and
// neighbouring text folds, so a Concat of one thing is that thing.
func Concat(parts ...Term) Term {
	flat := make([]Term, 0, len(parts))
	for _, p := range parts {
		if p.origin == OfJoin {
			for _, inner := range p.parts {
				flat = appendPart(flat, inner)
			}
			continue
		}
		flat = appendPart(flat, p)
	}
	switch len(flat) {
	case 0:
		return Term{}
	case 1:
		return flat[0]
	}
	return Term{origin: OfJoin, parts: flat}
}

func appendPart(flat []Term, p Term) []Term {
	if p.origin == OfText {
		if p.text == "" {
			return flat // nothing to add, and an empty part is not a part
		}
		if n := len(flat); n > 0 && flat[n-1].origin == OfText {
			flat[n-1] = Term{origin: OfText, text: flat[n-1].text + p.text}
			return flat
		}
	}
	return append(flat, p)
}

// Origin is where this term's value comes from.
func (t Term) Origin() Origin { return t.origin }

// Field is the attribute this term reads, segments joined; empty unless it
// reads one.
func (t Term) Field() string { return strings.Join(t.key, "/") }

// Key is the attribute this term reads, in segments.
func (t Term) Key() []string { return append([]string{}, t.key...) }

// Literal is the fixed text this term states, and whether it states any: an op
// a lowering writes is a verb only if its term is text.
func (t Term) Literal() (string, bool) {
	if t.origin == OfText {
		return t.text, true
	}
	return "", false
}

// Parts is what a join is made of, in order. A term that is not a join has no parts —
// it IS one.
func (t Term) Parts() []Term {
	if t.origin != OfJoin {
		return nil
	}
	return append([]Term{}, t.parts...)
}

// Empty reports that this term says nothing: an omitted Carries, a Named nobody filled in.
func (t Term) Empty() bool { return t.origin == OfText && strings.TrimSpace(t.text) == "" }

// Equal compares two terms as statements, not as the strings they produce.
func (t Term) Equal(o Term) bool {
	if t.origin != o.origin || t.text != o.text ||
		len(t.key) != len(o.key) || len(t.parts) != len(o.parts) {
		return false
	}
	for i := range t.key {
		if t.key[i] != o.key[i] {
			return false
		}
	}
	for i := range t.parts {
		if !t.parts[i].Equal(o.parts[i]) {
			return false
		}
	}
	return true
}

// Read is one field a term reads: which attribute, and out of what.
type Read struct {
	// Field is the attribute's key, joined — "attempts", "memory/size".
	Field string
	// Origin is one of OfSource, OfElement, OfNextElement.
	Origin Origin
}

// String is the pre-term spelling, still how a read is printed in diagnostics
// (`{each.attemps}`).
func (r Read) String() string {
	switch r.Origin {
	case OfElement:
		return "{each." + r.Field + "}"
	case OfNextElement:
		return "{each.next." + r.Field + "}"
	}
	return "{" + r.Field + "}"
}

// Sole is the one field this term reads, when it reads exactly one and states
// nothing else: a value carried rather than built, keeping its type and able to
// be given back.
func (t Term) Sole() (Read, bool) {
	if !t.origin.reads() {
		return Read{}, false
	}
	return Read{Field: t.Field(), Origin: t.origin}, true
}

// Reads is every field this term reads, in order, asked of the schema alone.
func (t Term) Reads() []Read {
	if r, ok := t.Sole(); ok {
		return []Read{r}
	}
	var out []Read
	for _, p := range t.parts {
		out = append(out, p.Reads()...)
	}
	return out
}

// String is the term as it reads, in the template spelling (`{name}`,
// `{each.run}`, `{each.next.name}`, `{scope}`): output only.
func (t Term) String() string {
	switch t.origin {
	case OfText:
		return t.text
	case OfScope:
		return "{scope}"
	case OfJoin:
		var b strings.Builder
		for _, p := range t.parts {
			b.WriteString(p.String())
		}
		return b.String()
	}
	return Read{Field: t.Field(), Origin: t.origin}.String()
}

// env is what a term resolves against: the resource, the element, the next
// element, and where the lowering put what it made, assigned by the plan.
type env struct {
	res, each, next map[string]any
	scope           string
}

// value is this term's value with its type intact, which only a term reading one
// field has, so a scalar arrives in wire form and is put back by its own inverse.
func (t Term) value(e env) (any, bool) {
	src := e.res
	switch t.origin {
	case OfText:
		return t.text, true
	case OfScope:
		return e.scope, e.scope != ""
	case OfJoin:
		return t.fill(e)
	case OfElement:
		src = e.each
	case OfNextElement:
		src = e.next
	}
	if src == nil {
		return nil, false
	}
	// A key addresses one level per segment.
	for _, seg := range t.key[:max(len(t.key)-1, 0)] {
		next, ok := src[seg].(map[string]any)
		if !ok {
			return nil, false
		}
		src = next
	}
	if len(t.key) == 0 {
		return nil, false
	}
	v, ok := src[t.key[len(t.key)-1]]
	return v, ok
}

// fill is the term as a string, and whether every part of it could be filled in. A part
// that resolves to nothing is a part that could not be filled: a step with no `on_error`
// has no failure queue to name, which is the common case rather than a mistake.
func (t Term) fill(e env) (string, bool) {
	if t.origin == OfJoin {
		var b strings.Builder
		for _, p := range t.parts {
			s, ok := p.fill(e)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		}
		return b.String(), true
	}
	v, ok := t.value(e)
	if !ok {
		return "", false
	}
	s, ok := scalarString(v)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// A term either fills or it does not, and the caller decides: set() drops the
// field, writes() drops the write, lower() refuses an unnamed resource.

// scalarString is a compiled value as a name can carry it.
func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	case map[string]any:
		// A single-valued object reads as its value: a resolved reference is `{kind:
		// id}`. Anything with more is a block.
		if len(t) != 1 {
			break
		}
		for _, only := range t {
			if s, ok := only.(string); ok {
				return s, true
			}
		}
	}
	// A field that is a block or a list has no place in a name; leaving it unfilled makes
	// that visible rather than printing "map[]".
	return "", false
}

// Composition read backwards: a join gives back its one unknown part when
// everything around it is known, the known prefix's length being the split.
// Known means text the lowering wrote, or the lowered resource's own name.

// composed reports the one part of a join unknown at read-back, with what stands
// before and after it.
func (t Term) composed(nameField string) (part Term, before, after Term, ok bool) {
	if t.origin != OfJoin {
		return Term{}, Term{}, Term{}, false
	}
	at := -1
	for i, p := range t.parts {
		if p.anchored(nameField) {
			continue
		}
		if at >= 0 {
			return Term{}, Term{}, Term{}, false // two unknowns: neither has an edge
		}
		at = i
	}
	if at < 0 {
		return Term{}, Term{}, Term{}, false // nothing composed IN; nothing to take out
	}
	return t.parts[at], Concat(t.parts[:at]...), Concat(t.parts[at+1:]...), true
}

// anchored reports that this part's value is known when a lowering is read backwards.
func (t Term) anchored(nameField string) bool {
	return t.origin == OfText ||
		(t.origin == OfSource && nameField != "" && t.Field() == nameField)
}

// uncompose takes the composed part back out of a value the lowering produced, given the
// terms that stand before and after it. It is exact: the anchors are filled in, their
// lengths are the cut, and what is between them is returned untouched.
func uncompose(value string, before, after Term, sourceName, nameField string) (string, bool) {
	e := env{res: map[string]any{}}
	if nameField != "" {
		e.res[nameField] = sourceName
	}
	head, ok := before.fill(e)
	if !ok && !before.Empty() {
		return "", false
	}
	tail, ok := after.fill(e)
	if !ok && !after.Empty() {
		return "", false
	}
	if !strings.HasPrefix(value, head) || !strings.HasSuffix(value[len(head):], tail) {
		return "", false
	}
	return value[len(head) : len(value)-len(tail)], true
}
