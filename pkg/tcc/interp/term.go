package interp

import (
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// A TERM, NOT A TEMPLATE.
//
// Everything a lowering states about a value it will produce — what the derived resource
// is called, what one line says it carries, what goes into one of its fields, what it
// writes into somebody else's — used to be a string with `{}` in it, scanned byte by byte
// at plan time. `"{name}_{each.name}"` was one string; what it MEANT was three things
// glued together, and the glue is where everything went wrong:
//
//   - A KEY SPELLED ITS OWN PATH. `{memory/size}` puts a "/" in a name, which is the one
//     thing §4 of the vocabulary's own rules forbids everywhere else: hierarchy is
//     SEGMENTS the engine joins, never a string spelling them. `Attr(Key("memory","size"))`
//     says the same thing without the vocabulary ever writing the separator.
//
//   - THE PLAN'S OWN FACTS WERE MAGIC WORDS. `{into}`, `{produced}` and `{scope}` are not
//     fields of anything; they were smuggled through a strings.Replacer and then had to be
//     skipped by name in the build-time input check — a hardcoded list of three strings
//     that a fourth would have had to be added to. They are terms now, so nothing has to
//     remember to skip them.
//
//   - A COMPOSED VALUE HID ITS OWN PARTS. This is the one that matters. Reading
//     `checkout_reserve` back into `checkout` + `_` + `reserve` means guessing where it
//     splits, and the guess is wrong the moment either half contains an underscore — so
//     reversal.go had to declare composition one-way and leave every composed field in the
//     shell. A Concat STATES its parts. The prefix is a term whose value is known, so
//     what is left after it is the part that was composed IN, exactly, with nothing
//     guessed. Invertibility becomes a property the DSL states rather than one a scanner
//     infers from punctuation.
//
// The set of terms is CLOSED, and the type is a struct with unexported fields for the
// same reason Lowering is: this DSL is written in terms, not in literals, and a term
// nobody outside this package can construct is a term every consumer can exhaust.
//
// It stays DATA. Not a closure — Named's own doc has said why since the first cut, "so a
// lowering can be printed" — and every question a consumer asks (what does this read,
// what is it made of, is it one value or a built string) is answered without evaluating
// anything against any config.

// Origin is where one term's value comes from. Six, and there will not be a seventh
// without a decision: each is a place the interpreter can actually reach, and the ones
// that read config reach exactly three levels — the resource, the element, the element
// after it, and no further, because arbitrary navigation is a query language.
//
// There is deliberately no term for "the kind this lowering produces" or "what it called
// it". Those were `{into}` and `{produced}`, and a term for either would be the same
// placeholder in a type's clothing: a token threaded through a resolver that puts a
// string back. They are not values a lowering states — they are facts the plan holds in a
// local variable — so a write that lands on what this lowering made says so
// STRUCTURALLY, by naming no other target (AtProduced), and the plan puts its own two
// strings in front of the path. Nothing is substituted anywhere.
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
	// OfScope is where this lowering put what it produced, absolute from the project
	// root. The one fact about the plan that is a VALUE rather than a target: an entry
	// written onto somebody else's resource has to say where the thing it names lives,
	// and no field of the config being lowered knows.
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

// Term is one value a lowering states. Build one with Text, Attr, Each, Next, Scope or
// Concat; the zero Term says nothing, which is what an omitted Carries is.
type Term struct {
	origin Origin
	// key is the attribute this reads, IN SEGMENTS — one level of the compiled object
	// each. Segments rather than a joined string on purpose: a key may never contain a
	// "/" (the vocabulary's own §4), so a term that held one would be a name spelling a
	// path, and reading it would mean splitting a string again. The engine joins them
	// when a key has to be PRINTED; nothing here ever takes one apart.
	key   []string // OfSource, OfElement, OfNextElement
	text  string   // OfText
	parts []Term   // OfJoin
}

// Text is fixed text: a separator, a suffix, a value that is the same for every resource
// this lowering runs over.
func Text(s string) Term { return Term{origin: OfText, text: s} }

// Attr is a field of the resource being lowered — `{name}` as it used to be written.
//
// It takes the same two spellings every other term that NAMES an attribute takes, a
// plain key or Key(...) segments, so a nested field is `Attr(Key("memory", "size"))` and
// the "/" between the levels is the engine's to write.
func Attr[T string | engine.AKey](field T) Term {
	return Term{origin: OfSource, key: segments(field)}
}

// Each is a field of the current element of the list PerEach names.
func Each[T string | engine.AKey](field T) Term {
	return Term{origin: OfElement, key: segments(field)}
}

// Next is a field of the element AFTER the current one. One step, never two: an ordered
// list whose elements can only see themselves lowers into independent things, and a chain
// — step, then the step after it — is most of the reason a list is ordered at all. Going
// further is a query language, and the shapes that need one (a branch, a join) need more
// than this anyway.
func Next[T string | engine.AKey](field T) Term {
	return Term{origin: OfNextElement, key: segments(field)}
}

// Scope is where this lowering put what it produced, absolute from the project root.
//
// It is the one fact about the plan that a lowering can state as a VALUE, and it earns
// that because it is a value: an entry written onto a resource somebody else wrote has to
// say which namespace the thing it names lives in, and nothing in the config being lowered
// knows — Lowerable decided it. The plan hands it over by assignment, out of the same
// local it built the placement from.
//
// Empty for a vocabulary that declares no Lowerable, which drops any write that uses it —
// so nothing changes for a vocabulary that scopes nothing.
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

// Concat is several terms, one after the other.
//
// It is the term the string templates could not be: `"{name}_{each.name}"` and
// `Concat(Attr("name"), Text("_"), Each("name"))` produce the same three characters of
// output and are not the same statement. The second one still knows, afterwards, that the
// underscore was a separator it wrote rather than one that came out of a name — which is
// the whole of what reversal.go needs and could not have.
//
// Nested joins are flattened and neighbouring text is folded together, so the parts of a
// Concat are its parts however it was written; a Concat of one thing IS that thing, which
// keeps "reads exactly one field" a property of the value rather than of the spelling.
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

// Field is the attribute this term reads, by its identity — the segments joined, which is
// how every other part of the engine names one. Empty unless it reads one.
func (t Term) Field() string { return strings.Join(t.key, "/") }

// Key is the attribute this term reads, in segments.
func (t Term) Key() []string { return append([]string{}, t.key...) }

// Literal is the fixed text this term states, and whether it states any. It is what a
// consumer asks instead of scanning a value for braces: an op a lowering writes is a verb
// only if the term is text, and a term that is not is one the plan fills in.
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

// Equal compares two terms as STATEMENTS, not as the strings they would produce.
// `Concat(Attr("name"), Text("_x"))` and `Text("checkout_x")` print the same for one
// resource and are different lowerings.
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

// String is how the read was written before terms, which is still how it is PRINTED: an
// input a lowering names but the shell does not have is reported as `{each.attemps}`, and
// a value that could not be filled is left looking like that in the plan.
func (r Read) String() string {
	switch r.Origin {
	case OfElement:
		return "{each." + r.Field + "}"
	case OfNextElement:
		return "{each.next." + r.Field + "}"
	}
	return "{" + r.Field + "}"
}

// Sole is the one field this term reads, when it reads exactly one and states nothing
// else. It is the STRUCTURAL replacement for "the template is one `{key}` and nothing
// around it": a value being carried rather than a string being built, which is what lets
// a scalar keep its type on the way through and what decides whether a lowering can give
// the value back.
func (t Term) Sole() (Read, bool) {
	if !t.origin.reads() {
		return Read{}, false
	}
	return Read{Field: t.Field(), Origin: t.origin}, true
}

// Reads is every field this term reads, in the order it states them, descending into a
// join. Asked of the schema with no config in the room, which is what the build-time
// check needs.
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

// String is the term as it reads. The spelling is the one the string templates used —
// `{name}`, `{each.run}`, `{each.next.name}`, `{scope}` — because it is a good spelling
// and because it is what already appears in diagnostics, in the record of what a lowering
// declared, and in the plan where a value could not be filled. It is OUTPUT only: nothing
// reads one of these back.
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

// env is everything a term can be resolved against: the resource being lowered, the
// element, the one after it, and where the lowering put what it made.
//
// The plan ASSIGNS these. Nothing is substituted into anything — resolving a term is a
// switch on its origin and a read out of one of these three maps, which is the whole of
// what replaced a byte-by-byte scan for braces.
type env struct {
	res, each, next map[string]any
	scope           string
}

// value is this term's value with its TYPE intact, which only a term that reads exactly
// one field has — a join has already built a string. It is what a scalar field takes, so
// a Bytes carried out of one resource arrives in the next as a byte count and is put back
// into authored form by the scalar's own inverse.
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
	// A key addresses a LEVEL PER SEGMENT — `memory/embedder/name` is three levels of the
	// compiled resource — and the segments were stated as segments, so this walks them.
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

// show is the term filled in, with any part that could NOT be filled left looking exactly
// as it was declared. A field a resource does not carry shows up in the plan as a visible
// `{each.attempts}` rather than as a silently shortened name.
//
// It is RENDERING, not resolution: every part is resolved by the switch in value(), and a
// part that resolved to nothing is printed by String(). Nothing here reads a `{` back.
func (t Term) show(e env) string {
	if t.origin == OfJoin {
		var b strings.Builder
		for _, p := range t.parts {
			b.WriteString(p.show(e))
		}
		return b.String()
	}
	if s, ok := t.fill(e); ok {
		return s
	}
	return t.String()
}

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
		// A SINGLE-VALUED OBJECT READS AS ITS VALUE. One level holding one scalar is a
		// value that was given a name — a resolved reference is exactly that, `{kind:
		// id}` — and a term or a When asking for the field means the value, not the shape
		// it is stored in. Anything with more in it is a block, and falls through.
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

// COMPOSITION, READ BACKWARDS.
//
// A join gives one of its parts back when everything AROUND that part is known. `checkout`
// + `_` + a step name is `checkout_reserve`; knowing the first two, what is left after
// them is the third, whole, whatever it contains. Nothing is guessed and nothing is
// searched for — the length of the known prefix is the split.
//
// What counts as known is deliberately narrow: text the lowering wrote, and the name of
// the resource being lowered, which is not config the author put in the document but what
// the document IS, so a shell has it whatever else it drops. Any other field would make
// this circular — whether a shell still holds it is decided by this very analysis.

// composed reports the ONE part of a join that is not known at read-back, together with
// what stands before and after it. Not a join, no unknown part, or more than one, and
// there is nothing to take back.
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
