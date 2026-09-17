package engine

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// errorWithLocation formats an error message with file location information from
// a Query if available, as a *LocatedError so a consumer can read the position
// off the error instead of parsing it. Rendering is unchanged:
// "file:line:column: message", or just "message" with no location.
func errorWithLocation(query *yaseer.Query, format string, args ...any) error {
	filePath, line, column := query.Location()
	return located(filePath, line, column, fmt.Errorf(format, args...))
}

// simplifyYAMLError extracts user-friendly information from low-level YAML parsing errors.
// It removes technical details like "decode(*string) failed" and "yaml: unmarshal errors".
func simplifyYAMLError(err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Check if this is a YAML parsing error with technical details
	if strings.Contains(errStr, "decode(") || strings.Contains(errStr, "yaml: unmarshal errors") {
		// Extract location information if present
		// Format: "decode(*string) failed in file '/path' at line X, column Y: yaml: unmarshal errors:\n  line X: cannot unmarshal !!map into string"
		// We want to simplify this to just indicate the type mismatch or missing value

		// Check for type mismatch patterns
		if strings.Contains(errStr, "cannot unmarshal !!map into") {
			// This means we're trying to read a scalar value but got a map/object
			// The user-friendly message is that the field is missing or has wrong structure
			return errors.New("field is missing or has incorrect structure")
		}

		// For other YAML errors, return a generic message
		return errors.New("invalid YAML format")
	}

	return err
}

// wrapErrorWithLocation wraps an existing error with location information from a Query if available.
// If the underlying error already contains location information, it is not duplicated.
// Low-level YAML parsing errors are simplified before wrapping.
func wrapErrorWithLocation(query *yaseer.Query, err error, context string) error {
	if err == nil {
		return nil
	}

	// Simplify low-level YAML errors before wrapping
	err = simplifyYAMLError(err)

	wrapped := fmt.Errorf("%s: %w", context, err)

	// If the error already carries location info, don't duplicate it.
	if strings.Contains(err.Error(), "(at ") {
		return wrapped
	}

	filePath, line, column := query.Location()
	return located(filePath, line, column, wrapped)
}

func (n *Node) Map() map[string]any {
	// Pre-allocate map with known capacity (4 fields)
	m := make(map[string]any, 4)
	m["group"] = n.Group
	m["match"] = stringify(n.Match)
	m["attributes"] = n.attributesToMap()
	m["children"] = n.childrenToSlice()
	return m
}

func (n *Node) ChildMatch(name string) (*Node, error) {
	for _, c := range n.Children {
		switch pitm := c.Match.(type) {
		case string:
			if pitm == name {
				return c, nil
			}
		case StringMatcher:
			if pitm.Match(name) {
				return c, nil
			}
		}
	}
	return nil, errors.New("not found")
}

func (n *Node) attributesToMap() map[string]any {
	ret := make(map[string]any, len(n.Attributes))
	for _, attr := range n.Attributes {
		// Pre-allocate map with estimated capacity based on possible fields
		estimatedCapacity := 2 // type is always present
		if attr.Captures {
			estimatedCapacity++
		}
		if attr.Default != nil {
			estimatedCapacity++
		}
		if len(attr.Path) > 0 {
			estimatedCapacity++
		}
		if len(attr.Compat) > 0 {
			estimatedCapacity++
		}
		m := make(map[string]any, estimatedCapacity)
		m["type"] = attr.Type.String()
		if attr.Captures {
			m["key"] = true
		}
		if attr.Default != nil {
			m["default"] = attr.Default
		}
		if len(attr.Path) > 0 {
			m["path"] = stringify(attr.Path)
		}
		if len(attr.Compat) > 0 {
			m["compat"] = stringify(attr.Compat)
		}
		ret[attr.Name] = m
	}
	return ret
}

func (n *Node) childrenToSlice() []any {
	ret := make([]any, len(n.Children))
	for i, node := range n.Children {
		ret[i] = node.Map()
	}
	return ret
}

// inferPathQuery walks a declared path against the document, resolving each
// matcher segment to the key actually present. It returns the RESOLVED segments
// so a caller can report where it really looked ("streaming/size"), not the
// declared shape — the two differ exactly where a matcher is involved.
// ambiguousPath marks the one resolution failure that is a CONFLICT rather than an
// absence: two branches of a switch open at once.
const ambiguousPath = "ambiguous path"

// walk is what a load carries down that the schema cannot state: the per-compile
// decisions. `strict` was threaded alone; `env` joins it rather than becoming a
// second parameter on five signatures.
type walk struct {
	strict bool
	// env is the environment this compile is for, "" for none. A vocabulary that
	// never sets one is unaffected by every line below that reads it — which is why
	// per-environment resolution needs no opt-in: v1 passes "" and resolves exactly
	// as it did.
	env string
	// envs is every environment the project DECLARES, and it is a different question
	// from `env`. One document serves them all, so a document written for three
	// environments carries all three spellings at once and each compile reads one of
	// them — which means strict has to know the whole set, or production's compile
	// rejects preview's line as an unknown key. The set is closed and declared, so
	// `id_preview` is still a typo and so is `timeout_prevew`.
	envs []string
}

// envPath is attr's path with the environment appended to its LAST segment —
// `execution/memory` for `preview` is `execution/memory_preview`. nil when there is
// nothing to suffix: no environment, or a path whose last segment is an Either
// branch, which is a key the document chooses rather than one the schema names.
func envPath(path []StringMatch, env string) []StringMatch {
	if env == "" || len(path) == 0 {
		return nil
	}
	last, ok := path[len(path)-1].(string)
	if !ok {
		return nil
	}
	out := make([]StringMatch, len(path))
	copy(out, path)
	out[len(out)-1] = last + "_" + env
	return out
}

func inferPathQuery(path []StringMatch, query *yaseer.Query) (*yaseer.Query, []string, error) {
	resolved := make([]string, 0, len(path))
	var cachedList []string
	var cacheValid bool

	for _, itm := range path {
		switch pitm := itm.(type) {
		case string:
			query = query.Get(pitm)
			resolved = append(resolved, pitm)
			// Invalidate cache since query state changed
			cacheValid = false
		case StringMatcher:
			// Reuse cached list if available and query state hasn't changed
			var list []string
			var err error
			if cacheValid && cachedList != nil {
				list = cachedList
			} else {
				list, err = query.List()
				if err != nil {
					return nil, nil, wrapErrorWithLocation(query, err, "list path matches failed")
				}
				// Cache the list for potential reuse (only valid until query state changes)
				cachedList = list
				cacheValid = true
			}

			// Exactly one key may match. Taking "the first" would be a coin
			// flip: mapKeys returns document keys in unspecified order (see
			// yaseer/utils.go), so a document that opens BOTH branches of an
			// Either would resolve differently between runs — and differently
			// from any consumer that enumerates the branches statically.
			var matched []string
			for _, l := range list {
				if pitm.Match(l) {
					matched = append(matched, l)
				}
			}
			switch len(matched) {
			case 0:
				return nil, nil, errorWithLocation(query, "can't find match for path")
			case 1:
			default:
				slices.Sort(matched) // stable message, whatever the map order was
				return nil, nil, errorWithLocation(query,
					"%s: %s each match, and only one may be set", ambiguousPath, strings.Join(matched, " and "))
			}
			query = query.Get(matched[0])
			resolved = append(resolved, matched[0])
			cacheValid = false
		}
	}

	return query, resolved, nil
}

func (n *Node) hasRequiredAttributes() bool {
	for _, attr := range n.Attributes {
		if attr.Required {
			return true
		}
	}
	return false
}

// hasScalar reports that the query resolves to a value at all — used to tell a
// field that is absent (fine, unless required) from one that is present with a
// value the declared type cannot take (always an error).
func hasScalar(aq *yaseer.Query) bool {
	var raw any
	if aq.Value(&raw) != nil {
		return false
	}
	switch raw.(type) {
	case nil, map[string]any, map[any]any:
		return false // a missing leaf, or a sub-tree rather than a value
	}
	return true
}

// coerced retries a failed typed read as a generic scalar and converts it to the
// declared type where the notation is merely different (an unquoted id into a
// string field, a quoted number into a numeric one). See coerce.go.
func coerced(aq *yaseer.Query, t Type) (any, bool) {
	var raw any
	if aq.Value(&raw) != nil {
		return nil, false
	}
	return coerceScalar(raw, t)
}

func getValue(aq *yaseer.Query, attr *Attribute) (val any, err error) {
	switch attr.Type {
	case TypeInt:
		var v int
		err = aq.Value(&v)
		if err != nil {
			if c, ok := coerced(aq, TypeInt); ok {
				v, err = c.(int), nil
			} else {
				err = wrapErrorWithLocation(aq, err, fmt.Sprintf("failed to get int value for attribute '%s'", attr.Name))
			}
		}
		val = v
	case TypeBool:
		var v bool
		err = aq.Value(&v)
		if err != nil {
			err = wrapErrorWithLocation(aq, err, fmt.Sprintf("failed to get bool value for attribute '%s'", attr.Name))
		}
		val = v
	case TypeFloat:
		var v float64
		err = aq.Value(&v)
		if err != nil {
			if c, ok := coerced(aq, TypeFloat); ok {
				v, err = c.(float64), nil
			} else {
				err = wrapErrorWithLocation(aq, err, fmt.Sprintf("failed to get float value for attribute '%s'", attr.Name))
			}
		}
		val = v
	case TypeString:
		var v string
		err = aq.Value(&v)
		if err != nil {
			if c, ok := coerced(aq, TypeString); ok {
				v, err = c.(string), nil
			} else {
				err = wrapErrorWithLocation(aq, err, fmt.Sprintf("failed to get string value for attribute '%s'", attr.Name))
			}
		}
		val = v
	case TypeStringSlice:
		var v []string
		err = aq.Value(&v)
		if err != nil {
			err = wrapErrorWithLocation(aq, err, fmt.Sprintf("failed to get string slice value for attribute '%s'", attr.Name))
		}
		val = v
	default:
		err = wrapErrorWithLocation(aq, errors.ErrUnsupported, fmt.Sprintf("unsupported type for attribute '%s'", attr.Name))
	}
	return
}

// requiredHere reports whether attr's RequiredWhen condition holds for the
// resource being loaded: the discriminator it names currently has one of the
// values that make it required. The discriminator is read from the document
// rather than from the partly-built object, so it does not depend on attribute
// declaration order. An absent or unreadable discriminator means the condition
// cannot hold — a resource with no trigger type owes no trigger-specific field.
func requiredHere(n *Node, attr *Attribute, query *yaseer.Query) bool {
	if field, ok := attr.Meta["requiredUnless"].(string); ok {
		return requiredUnlessHere(n, field, query)
	}
	c, ok := attr.Meta["requiredWhen"].(ConditionSpec)
	if !ok {
		return false
	}
	return conditionHolds(n, c, query)
}

// defaultHere is whether an absent attribute's default belongs in THIS document.
//
// A DEFAULT UNDER A BRANCH NOBODY TOOK IS A VALUE FILED IN THE WRONG PLACE, and it is
// worse than a stray key: a discriminated kind reads its type off which block exists, so
// materialising `queue/visibility_timeout` on a resource whose block is `log:` invents
// the queue branch and makes the type ambiguous. The same defect the vocabulary refuses
// to let an AUTHOR write — `postgres: { size: 1GB }` is an unknown key rather than a
// number under a branch nobody took — the loader was creating for itself.
//
// An attribute with no `defaultWhen` keeps its default unconditionally, which is every
// attribute that had one before this existed.
func defaultHere(n *Node, attr *Attribute, query *yaseer.Query) bool {
	c, conditional := attr.Meta["defaultWhen"].(ConditionSpec)
	if !conditional {
		return true
	}
	return conditionHolds(n, c, query)
}

// conditionHolds evaluates one ConditionSpec against the document — the shared half of
// requiredHere and defaultHere, which ask the same question of the same sibling and
// differ only in what they do with the answer.
func conditionHolds(n *Node, c ConditionSpec, query *yaseer.Query) bool {
	for _, sib := range n.Attributes {
		if sib.Name != c.Field {
			continue
		}
		path := sib.Path
		if len(path) == 0 {
			path = []StringMatch{sib.Name}
		}
		// A Key() sibling has no value of its own: its path ends in the Either() and
		// the document holds the BRANCH there, so reading it as a string fails and the
		// condition silently never fires. What the condition means by that field is
		// which branch matched, which is the resolved last segment — the same thing
		// setAttributes files as the key.
		//
		// Without this, `RequiredWhen("engine/kind", "distributed")` was a promise the
		// loader did not keep: a distributed database with no size compiled clean.
		if sib.Captures {
			_, resolved, err := inferPathQuery(path, query)
			if err != nil || len(resolved) == 0 {
				return false
			}
			return slices.Contains(c.In, resolved[len(resolved)-1])
		}

		var v string
		if sq, _, err := inferPathQuery(path, query); err != nil || sq.Value(&v) != nil {
			cq, _, cerr := inferPathQuery(sib.Compat, query)
			if len(sib.Compat) == 0 || cerr != nil || cq.Value(&v) != nil {
				return false
			}
		}
		return slices.Contains(c.In, v)
	}
	return false
}

// parentExists reports whether the block a branching path hangs off is present in the
// document at all. A path whose Either() is at the top level hangs off the document
// root, which always exists — so a switch with no parent keeps its old behaviour and an
// unmatched key there is still an error.
func parentExists(n *Node, path []StringMatch, query *yaseer.Query) bool {
	if len(path) < 2 {
		return true
	}
	parent := path[:len(path)-1]
	pq, _, err := inferPathQuery(parent, query)
	if err != nil {
		return false
	}
	var v any
	if pq.Value(&v) != nil || v == nil {
		return false
	}
	switch v.(type) {
	case map[string]any, map[any]any:
		return true
	}
	// A SCALAR sits where the switch's block belongs, and that is legitimate only
	// when the vocabulary ALSO declares a value at exactly this key — an optional
	// switch whose default branch takes one argument, written as the value itself
	// (`source: agents/support`). That attribute reads it and validates it.
	//
	// Where nothing claims the bare key there is no such branch, so the scalar is a
	// mistake — `engine: postgres` where a `postgres:` block belongs — and this is
	// the error that catches it. Letting the scalar through there compiles a
	// database with no engine, a gateway with no host and a proxy forwarding to
	// nothing, all silently, because every field under the branch hangs off a
	// RequiredWhen that a captured-nothing switch never fires.
	return !claimsBareKey(n, parent)
}

// claimsBareKey reports whether some attribute reads a VALUE at exactly this path —
// the switch's own key rather than a key beneath it.
func claimsBareKey(n *Node, parent []StringMatch) bool {
	if n == nil {
		return false
	}
	for _, a := range n.Attributes {
		if a.Captures || len(a.Path) != len(parent) {
			continue
		}
		same := true
		for i, seg := range a.Path {
			lit, ok := seg.(string)
			if !ok || lit != literalOf(parent[i]) {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// literalOf is the plain-string form of a path segment, or "" for a dynamic one
// (Either/All), which never equals a literal.
func literalOf(m StringMatch) string {
	s, _ := m.(string)
	return s
}

// isBlank reports a value that carries no information: only strings and lists
// can be blank — a false bool or a zero int are legitimate values.
func isBlank(val any) bool {
	switch v := val.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []string:
		return len(v) == 0
	case []any:
		return len(v) == 0
	}
	return false
}

// requiredUnlessHere reports whether a RequiredUnless field is required right
// now: it is, unless the named sibling reads as true. An absent or unreadable
// discriminator means required — an unset bool is false — which is the opposite
// default to requiredHere's condition, and the reason the two are separate.
func requiredUnlessHere(n *Node, field string, query *yaseer.Query) bool {
	for _, sib := range n.Attributes {
		if sib.Name != field {
			continue
		}
		path := sib.Path
		if len(path) == 0 {
			path = []StringMatch{sib.Name}
		}
		var v bool
		if sq, _, err := inferPathQuery(path, query); err == nil && sq.Value(&v) == nil {
			return !v
		}
		// the fixtures author useRegex at its legacy location
		if cq, _, err := inferPathQuery(sib.Compat, query); len(sib.Compat) > 0 && err == nil && cq.Value(&v) == nil {
			return !v
		}
		return true
	}
	return false // names an attribute this group does not have
}

// requiredName is the authored path to report for a missing required field,
// using the segments the document actually resolved to when the declared path
// contains a matcher — "streaming/size", not the bare attribute name. Both
// enforcement paths must name the same thing or the agreement test cannot hold.
func requiredName(attr *Attribute, resolved []string) string {
	if p := fieldPath(attr); p != nil {
		return strings.Join(p, "/")
	}
	if len(resolved) > 0 {
		return strings.Join(resolved, "/")
	}
	return attr.Name
}

func setAttributes[T ObjectDataType](n *Node, obj object.Object[T], query *yaseer.Query, w walk) error {
	if len(n.Attributes) == 0 {
		return nil
	}

	for _, attr := range n.Attributes {
		// defAttr resolves this from the key; an Attribute built as a struct literal
		// (tests, and anything assembled by hand) still needs the fallback.
		if len(attr.Path) == 0 {
			attr.Path = []StringMatch{attr.Name}
		}
		// THE ENVIRONMENT'S VALUE FIRST. `memory_preview` beside `memory` is the same
		// field said for one environment, so it is a path tried BEFORE the declared
		// one — the mirror of Compat below, which is tried after.
		//
		// PRESENCE decides, not validity. If something is written at the suffixed key
		// the attribute reads THERE, and a wrong type or an empty value is reported
		// against the override's own line, rather than falling back to the base and
		// deploying a value nobody chose. `resolved` follows, so every message names
		// the key the author actually wrote.
		path := attr.Path
		if attr.Meta["perEnv"] == true {
			if ep := envPath(path, w.env); ep != nil {
				if eq, _, eerr := inferPathQuery(ep, query); eerr == nil && hasScalar(eq) {
					path = ep
				}
			}
		}
		aq, resolved, err := inferPathQuery(path, query)
		if err != nil {
			// AN AMBIGUOUS PATH IS NEVER A MISSING ONE. A switch with two of its
			// branches open is a conflict, and the compat fallback swallowed it: an
			// attribute with no legacy location resolves the empty path to the document
			// root without error, so the conflict was reported later, and elsewhere, as
			// "has an empty key". A path that simply is not there still falls through —
			// an optional block that nobody wrote is not a mistake.
			if strings.Contains(err.Error(), ambiguousPath) {
				return err
			}
			aq, resolved, err = inferPathQuery(attr.Compat, query)
			if err != nil {
				return wrapErrorWithLocation(query, err, fmt.Sprintf("attribute '%s' path resolution failed", attr.Name))
			}
		}

		if attr.Captures {
			if len(resolved) > 0 {
				switch o := obj.(type) {
				case object.Object[object.Refrence]:
					place(o, attr, object.Refrence(resolved[len(resolved)-1]))
				}
				continue
			}
			// Nothing matched the Either(). Two very different documents reach here and
			// they were being given the same answer:
			//
			//	handler:              the block is THERE and names something the switch
			//	    kv: {…}           does not have — a mistake, and the error that says
			//	                      "empty key" is the one that catches it
			//
			//	(no liveness at all)  the block is ABSENT, which for an optional switch
			//	                      is a legitimate document: a workload with no
			//	                      liveness probe is simply never restarted
			//
			// Erroring on both is what made an Either()+Key() switch unusable on
			// anything optional, so every switch in the vocabulary sits on a mandatory
			// block — not by design, but because the others could not be written.
			//
			// The parent tells them apart: if the block exists, some key in it is wrong.
			if attr.Required || parentExists(n, attr.Path, query) {
				return errorWithLocation(aq, "attribute %s has an empty key", attr.Name)
			}
			continue
		}

		origAq := aq
		val, err := getValue(aq, attr)
		if err != nil {
			aq, _, err = inferPathQuery(attr.Compat, query)
			if err != nil {
				return wrapErrorWithLocation(query, err, fmt.Sprintf("attribute '%s' compat path resolution failed", attr.Name))
			}
			val, err = getValue(aq, attr)
		}

		// A value that is THERE but of the wrong type is a mistake, not an
		// omission: coercion already accepted every difference that is merely
		// notation, so what is left is a real disagreement about the field.
		// Reporting it beats the old behaviour of dropping it — a mistyped
		// domains list used to silently deploy an unroutable function.
		if err != nil && hasScalar(origAq) {
			filePath, line, column := origAq.Location()
			return located(filePath, line, column,
				fmt.Errorf("attribute '%s' has a value of the wrong type", requiredName(attr, resolved)))
		}

		if err != nil {
			if attr.Required || requiredHere(n, attr, query) {
				// For required attributes, format as: filepath:line:column: required attribute 'name'
				// Don't include underlying YAML processing error details
				filePath, line, column := aq.Location()
				return located(filePath, line, column, fmt.Errorf("required attribute '%s'", requiredName(attr, resolved)))
			}
			if attr.Default != nil && defaultHere(n, attr, query) {
				switch o := obj.(type) {
				case object.Object[object.Refrence]:
					place(o, attr, attr.Default)
				}
			}
			continue
		}

		if (attr.Required || requiredHere(n, attr, query)) && isBlank(val) {
			// Present but empty: an empty domains list routes nothing, an empty
			// entrypoint invokes nothing. Same defect as the absent key.
			filePath, line, column := aq.Location()
			return located(filePath, line, column, fmt.Errorf("required attribute '%s' is empty", requiredName(attr, resolved)))
		}

		if attr.Validator != nil {
			if err = attr.Validator(val); err != nil {
				// For validation errors, format as: filepath:line:column: message
				// Don't include "validation failed for attribute" wrapper, just the location and validator error
				filePath, line, column := aq.Location()
				return located(filePath, line, column, err)
			}
		}

		switch o := obj.(type) {
		case object.Object[object.Refrence]:
			place(o, attr, val)
		}
	}

	return nil
}

// WHERE A VALUE LANDS IN THE COMPILED OBJECT.
//
// `Path(...)` is the address, and the compiled object is a tree, so an attribute
// authored under a nested block compiles INTO that block: `Path("execution","call")`
// puts the value at execution -> call, two levels a CRDT merges independently. It used
// to land on a flat key named after the attribute, which is why a vocabulary that wanted
// the nesting to survive had to spell the path into the name — `execution-call` — and
// end up with one opaque leaf that can only be replaced whole.
//
// `Tag(...)` still overrides, as one segment: that is how a vocabulary whose WIRE format
// deliberately differs from its authored shape says so. v1 is exactly that — its
// compiled keys are flat by design and its consumers read them that way.
//
// A Captures() attribute is the odd one: its path ENDS in the Either it captured, so the
// branch name is the value rather than a level to write under. It lands beside the
// branch, under its own name.
func place(o object.Object[object.Refrence], attr *Attribute, val any) {
	segments := CompiledSegments(attr)
	last := len(segments) - 1
	if last == 0 {
		o.Set(segments[0], val)
		return
	}
	parent, err := o.CreatePath(segments[:last]...)
	if err != nil {
		o.Set(attr.Name, val) // the tree refused the path; better a flat key than a lost value
		return
	}
	parent.Set(segments[last], val)
}

// CompiledSegments is the key path an attribute's value is written at — one level of
// the compiled object per entry.
func CompiledSegments(attr *Attribute) []string {
	if len(attr.Segments) > 0 {
		return attr.Segments
	}
	// Single segment. NOT the Tag: a tagged attribute is renamed later, by
	// applyInstanceOps, and the gates that run in between (OnlyWhen) key off the name
	// it was loaded under. Landing on the tag here skips them.
	return []string{attr.Name}
}

// loadSequence reads a group authored as a YAML sequence. Each element goes through
// the SAME iterator as a name-keyed entry — types, defaults, required, validators and
// unknown-key rejection all apply, and each error points at the element's own line.
//
// What differs is the result. A name-keyed group becomes children the object model
// files under their names; a list has no names, and filing elements under "0", "1",
// "2" would leave every reader to sort them back — numerically, or "10" lands between
// "1" and "2" and the pipeline silently runs in the wrong order. So the sequence
// compiles to ONE ordered value, and the order is the document's.
func loadSequence[T ObjectDataType](n *Node, obj object.Object[T], query *yaseer.Query, w walk) error {
	name, ok := n.Match.(string)
	if !ok || len(n.Children) == 0 {
		return nil
	}
	at := query.Get(name)

	var doc any
	if at.Value(&doc) != nil {
		return nil // absent, which for a list means empty
	}
	// A mapping here is the shape mistake the DSL exists to catch: the entries would
	// load, in whatever order the map yielded, and the pipeline would run in an order
	// nobody wrote.
	if isMapping(doc) {
		filePath, line, column := at.Location()
		return located(filePath, line, column,
			fmt.Errorf("'%s' is an ordered list, not a set of named entries — write it as a sequence", name))
	}
	items, isList := doc.([]any)
	if !isList {
		filePath, line, column := at.Location()
		return located(filePath, line, column, fmt.Errorf("'%s' must be a list", name))
	}

	iter := n.Children[0]
	out := make([]any, 0, len(items))
	for i := range items {
		el, err := load[T](iter, at.Get(strconv.Itoa(i)), w)
		if err != nil {
			return err
		}
		out = append(out, el.Flat())
	}

	// Only the compiled (Refrence) object holds plain values; the seer-linked one
	// carries queries instead, and nothing reads a list through it. Same split
	// setAttributes already makes for defaults.
	if o, okType := obj.(object.Object[object.Refrence]); okType {
		o.Set(name, out)
	}
	return nil
}

func load[T ObjectDataType](n *Node, query *yaseer.Query, w walk, claimed ...map[string]bool) (object.Object[T], error) {
	obj := object.New[T]()

	// Before reading anything: where the vocabulary asks for it, a key this node
	// cannot read is a mistake rather than an extension. See strict.go — it used to
	// be dropped in silence, and it still is for a vocabulary that does not opt in.
	if w.strict {
		if err := checkKeys(n, query, w.envs); err != nil {
			return nil, err
		}
	}

	if !n.Group {
		if err := setAttributes(n, obj, query, w); err != nil {
			return nil, err
		}
		if !n.Nested {
			return obj, nil
		}
		// A nested resource's child groups live inside its OWN document, so they
		// walk the same query: no config leaf, no directory descent.
		return obj, loadChildren(n, obj, query, w, claimed...)
	}

	// file might or might not have config, so we ignore error
	err := setAttributes(n, obj, query.Get(LeafOf(n)), w)
	if err != nil && n.hasRequiredAttributes() {
		return nil, err
	}

	return obj, loadChildren(n, obj, query, w, claimed...)
}

// mergeInto folds one object's values and children into another, leaving what is
// already there alone: whoever wrote first declared it, and this is the second
// declaration arriving at the same key.
func mergeInto[T ObjectDataType](into, from object.Object[T]) {
	for key, val := range from.Flat() {
		if child, err := from.Child(key).Object(); err == nil {
			existing, err := into.CreatePath(key)
			if err != nil {
				continue
			}
			mergeInto(existing, child)
			continue
		}
		if any(into.Get(key)) == nil {
			if v, ok := val.(T); ok {
				into.Set(key, v)
			}
		}
	}
}

// claimedUnder is the set of keys this node's OWN attributes declare inside the child
// group named `key` — the second segment of every attribute path that starts there. The
// group must not read those as entries of its own.
func claimedUnder(n *Node, key string, envs []string) map[string]bool {
	var out map[string]bool
	claim := func(k string) {
		if out == nil {
			out = map[string]bool{}
		}
		out[k] = true
	}
	for _, a := range n.Attributes {
		if len(a.Path) < 2 {
			continue
		}
		first, ok := a.Path[0].(string)
		if !ok || first != key {
			continue
		}
		second, ok := a.Path[1].(string)
		if !ok {
			continue
		}
		claim(second)
		// AND ITS ENVIRONMENT SPELLINGS, where a block and an access GROUP share a
		// name. An agent's `model:` is both — the block naming which model answers,
		// and the group granting access to models — so what is not claimed here is
		// walked as an access entry. `model/name` is claimed and `model/name_preview`
		// was not, which made an override of a marked field read as an entry named
		// `name_preview` and fail with `required attribute 'ops'`: an error about a
		// shape the author never wrote.
		//
		// Only where the second segment is also the LAST: a suffix lands on the leaf,
		// so `engine/postgres/host` suffixes `host` and claiming `postgres_preview`
		// would reserve a branch key nothing writes.
		if len(a.Path) == 2 && a.Meta["perEnv"] == true {
			for _, env := range envs {
				if ep := envPath(a.Path, env); ep != nil {
					if last, ok := ep[len(ep)-1].(string); ok {
						claim(last)
					}
				}
			}
		}
	}
	return out
}

// loadChildren walks a node's child groups against the document at query. Shared by
// group nodes (children are entries one level down) and nested resources (children
// are keys of the resource's own document).
func loadChildren[T ObjectDataType](n *Node, obj object.Object[T], query *yaseer.Query, w walk, claimed ...map[string]bool) error {
	list, _ := query.List()
	// A resource-container has two channels below one instance: directories are the
	// resources it contains, while keys in its leaf document are the nested data of
	// the resource itself. A plain resource only has the latter and a plain container
	// only the former, so they never needed to be told apart before.
	hybrid := n.Group && n.Nested

	// A KEY AN ATTRIBUTE DECLARED IS NOT AN ENTRY OF THE GROUP THAT SHARES ITS NAME.
	//
	// A kind and an attribute may be called the same thing — `model` is a kind, and an
	// agent has a `model`. The attribute writes at model/name and model/namespace; the
	// access group generated for the kind holds one entry per target, keyed by the
	// target's name. Both live under `model`, and they do not collide unless the group
	// is allowed to read the attribute's own keys as entries — which it was, so
	// `model: {name: opus}` came back as an entry called "name" with no ops.
	//
	// Same rule as the leaf below, and for the same reason: this key belongs to
	// something else at this level.
	var mine map[string]bool
	if len(claimed) > 0 {
		mine = claimed[0]
	}

	// Also enumerate keys nested inside the level's config.yaml so
	// `DefineGroup("clouds", DefineIter(...))` matches a `clouds:` map
	// authored as nested YAML, not just as a `clouds/<key>.yaml` directory.
	// Filesystem entries take precedence.
	leaf := LeafOf(n)
	configList, _ := query.Get(leaf).List()
	inFS := make(map[string]bool, len(list))
	for _, l := range list {
		inFS[l] = true
	}

	tryMatch := func(itm *Node, key string) (string, bool) {
		switch i := itm.Match.(type) {
		case string:
			if i == key {
				return key, true
			}
		case StringMatcher:
			if i.Match(key) {
				return key, true
			}
		}
		return "", false
	}

	processChild := func(itm *Node, l string, childQuery *yaseer.Query) error {
		if mine[l] {
			return nil
		}
		match, matched := tryMatch(itm, l)
		if !matched {
			return nil
		}
		cobj, err := load[T](itm, childQuery, w, claimedUnder(n, l, w.envs))
		if err != nil {
			return err
		}
		// MERGE, never replace. Two declarations can land at one key — an attribute
		// writing at model/name and the access group generated for the kind `model`,
		// which holds one entry per target. Adding over the top made whichever ran
		// second the only survivor, silently.
		if into, err := obj.Child(match).Object(); err == nil {
			mergeInto(into, cobj)
			return nil
		}
		return obj.Child(match).Add(cobj)
	}

	for _, itm := range n.Children {
		if itm.Sequence {
			at := query
			if hybrid && itm.Nested {
				at = query.Get(leaf)
			}
			if err := loadSequence(itm, obj, at, w); err != nil {
				return err
			}
			continue
		}
		if !hybrid || !itm.Nested {
			for _, l := range list {
				// Only a GROUP keeps its own fields in a leaf document, so only a group
				// reserves that name. A nested resource reads its attributes from its own
				// document root and has no leaf at all — skipping the name there deleted a
				// legitimate child group, silently.
				if n.Group && l == leaf {
					continue
				}
				if err := processChild(itm, l, query.Get(l)); err != nil {
					return err
				}
			}
		}
		if !hybrid || itm.Nested {
			for _, l := range configList {
				// Filesystem entries take precedence — for the child that READS them.
				// Under a hybrid node the directory belongs to the resource group and the
				// document key to the kind's own group, so letting the directory suppress
				// the document key dropped the block in silence: an agent declaring
				// `function: {checkout: {ops: [invoke]}}` that lowers into a function of
				// its own kept the function and lost the tool.
				if (inFS[l] && !(hybrid && itm.Nested)) || (n.Group && l == leaf) {
					continue
				}
				if err := processChild(itm, l, query.Get(leaf).Get(l)); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
