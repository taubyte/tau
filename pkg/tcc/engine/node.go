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

	if strings.Contains(errStr, "decode(") || strings.Contains(errStr, "yaml: unmarshal errors") {
		// A scalar read that got a map: the field is missing or has the wrong
		// structure.

		if strings.Contains(errStr, "cannot unmarshal !!map into") {
			return errors.New("field is missing or has incorrect structure")
		}

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

	err = simplifyYAMLError(err)

	wrapped := fmt.Errorf("%s: %w", context, err)

	// If the error already carries location info, don't duplicate it.
	if strings.Contains(err.Error(), "(at ") {
		return wrapped
	}

	filePath, line, column := query.Location()
	return located(filePath, line, column, wrapped)
}

// ambiguousPath marks the one resolution failure that is a conflict rather than
// an absence: two branches of a switch open at once.
const ambiguousPath = "ambiguous path"

// walk is what a load carries down that the schema cannot state: the per-compile
// decisions.
type walk struct {
	// bound is the variables' values for this read, nil when it binds none; an empty
	// binding resolves as a schema with no variables does.
	bound Bound
	// vars is every declared variable with this project's values. One document
	// carries every value's spelling, so strict must know the whole set or one read
	// rejects another's line; `<key>_typo` is still a typo.
	vars Vars
}

// variantPath is attr's path with value appended to its last segment: `x/y`
// under `a` is `x/y_a`. A matcher spells its own suffixed form. An attribute
// capturing which alternative was taken does not vary and reads the bare key.
// nil when there is nothing to suffix: no value, or All(), which cannot spell
// itself.
func variantPath(path []StringMatch, value string) []StringMatch {
	if value == "" || len(path) == 0 {
		return nil
	}

	out := make([]StringMatch, len(path))
	copy(out, path)

	switch last := path[len(path)-1].(type) {
	case string:
		out[len(out)-1] = last + "_" + value
	case SuffixMatcher:
		// Only the matcher knows what it admits, so only it can spell its suffixed form.
		out[len(out)-1] = last.Suffixed(value)
	default:
		return nil
	}
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
			cacheValid = false
		case StringMatcher:
			var list []string
			var err error
			if cacheValid && cachedList != nil {
				list = cachedList
			} else {
				list, err = query.List()
				if err != nil {
					return nil, nil, wrapErrorWithLocation(query, err, "list path matches failed")
				}
				cachedList = list
				cacheValid = true
			}

			// Exactly one key may match: mapKeys returns keys in unspecified order, so
			// taking the first would resolve a document with two branches open differently
			// between runs.
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
				slices.Sort(matched)
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
	c, ok := attr.Meta["requiredWhen"].(ConditionSpec)
	if !ok {
		return false
	}
	return conditionHolds(n, c, query)
}

// defaultHere is whether an absent attribute's default belongs in this document.
// A default under a branch nobody took invents that branch, making a
// discriminated kind's type ambiguous. Without defaultWhen a default always
// applies.
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
		// A Captures() sibling has no value of its own: the document holds the branch
		// at its path, so the condition means which branch matched.
		if sib.Captures {
			_, resolved, err := inferPathQuery(path, query)
			if err != nil || len(resolved) == 0 {
				return false
			}
			return slices.Contains(c.In, resolved[len(resolved)-1])
		}

		var v string
		if sq, _, err := inferPathQuery(path, query); err != nil || sq.Value(&v) != nil {
			return false
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
	// A scalar where the switch's block belongs is legitimate only when the
	// vocabulary also declares a value at exactly this key (`source:
	// agents/support`). Otherwise it is a mistake, `engine: postgres` where a block
	// belongs, which would compile with every field under the branch silently
	// absent.
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

// variantOf is which of this attribute's variant keys the document writes, nil
// for none. Variables are tried in the root's Varying order. Two variants
// written at once is a conflict, as ambiguousPath is for a switch. There is no
// compound suffix.
func variantOf(attr *Attribute, query *yaseer.Query, w walk) ([]StringMatch, error) {
	if len(w.bound) == 0 {
		return nil, nil
	}
	by := VariesBy(attr)
	if len(by) == 0 {
		return nil, nil
	}

	var found []StringMatch
	var foundVia, foundAt string
	for _, name := range w.vars {
		if !slices.Contains(by, name.Name) {
			continue
		}
		value, isBound := w.bound[name.Name]
		if !isBound {
			continue
		}
		path := variantPath(attr.Path, value)
		if path == nil {
			continue
		}
		vq, resolved, err := inferPathQuery(path, query)
		if err != nil || !hasScalar(vq) {
			continue
		}
		at := strings.Join(resolved, "/")
		if found != nil {
			return nil, wrapErrorWithLocation(query, fmt.Errorf("attribute '%s' is written for both %s (%s) and %s (%s) — two answers, and nothing says which", attr.Name, foundVia, foundAt, name.Name, at), "conflicting variants")
		}
		found, foundVia, foundAt = path, name.Name, at
	}
	return found, nil
}

func setAttributes(n *Node, obj object.Object[object.Refrence], query *yaseer.Query, w walk) error {
	if len(n.Attributes) == 0 {
		return nil
	}

	for _, attr := range n.Attributes {
		// defAttr resolves this from the key; an Attribute built as a struct literal
		// (tests, and anything assembled by hand) still needs the fallback.
		if len(attr.Path) == 0 {
			attr.Path = []StringMatch{attr.Name}
		}
		// The variant's value first. Presence decides, not validity: a wrong value at
		// the suffixed key is reported against that line, never falling back to the
		// base.
		path := attr.Path
		if found, err := variantOf(attr, query, w); err != nil {
			return err
		} else if found != nil {
			path = found
		}
		aq, resolved, err := inferPathQuery(path, query)
		if err != nil {
			// An ambiguous path is never a missing one: two branches open is a conflict.
			if strings.Contains(err.Error(), ambiguousPath) {
				return err
			}
			aq, resolved = query, nil
		}

		if attr.Captures {
			if len(resolved) > 0 {
				switch o := obj.(type) {
				case object.Object[object.Refrence]:
					place(o, attr, object.Refrence(resolved[len(resolved)-1]))
				}
				continue
			}
			// Nothing matched the Either(). If the block exists, some key in it is wrong; if
			// it is absent, an optional switch is a legitimate document (a workload with no
			// liveness probe).
			if attr.Required || parentExists(n, attr.Path, query) {
				return errorWithLocation(aq, "attribute %s has an empty key", attr.Name)
			}
			continue
		}

		origAq := aq
		val, err := getValue(aq, attr)
		if err != nil {
			aq = query
		}

		// A value present but of the wrong type is a mistake: coercion already accepted
		// every difference of mere notation.
		if err != nil && hasScalar(origAq) {
			filePath, line, column := origAq.Location()
			return located(filePath, line, column,
				fmt.Errorf("attribute '%s' has a value of the wrong type", requiredName(attr, resolved)))
		}

		if err != nil {
			if attr.Required || requiredHere(n, attr, query) {
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
			// Present but empty: an empty domains list routes nothing.
			filePath, line, column := aq.Location()
			return located(filePath, line, column, fmt.Errorf("required attribute '%s' is empty", requiredName(attr, resolved)))
		}

		if attr.Validator != nil {
			if err = attr.Validator(val); err != nil {
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

// place writes a value at its Path in the compiled object, so a nested block
// compiles into nested levels a CRDT merges independently. A Captures()
// attribute's path ends in the Either it captured, so it lands beside the branch
// under its own name.
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
	return []string{attr.Name}
}

// loadSequence reads a group authored as a YAML sequence; each element goes
// through the same iterator as a name-keyed entry. It compiles to one ordered
// value: filing elements under "0", "1" would leave readers to sort them, and
// "10" lands between "1" and "2".
func loadSequence(n *Node, obj object.Object[object.Refrence], query *yaseer.Query, w walk) error {
	name, ok := n.Match.(string)
	if !ok || len(n.Children) == 0 {
		return nil
	}
	at := query.Get(name)

	var doc any
	if at.Value(&doc) != nil {
		return nil // absent, which for a list means empty
	}
	// A mapping would load in map order, running the pipeline in an order nobody
	// wrote.
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
		el, err := load(iter, at.Get(strconv.Itoa(i)), w)
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

func load(n *Node, query *yaseer.Query, w walk, claimed ...map[string]bool) (object.Object[object.Refrence], error) {
	obj := object.New[object.Refrence]()

	// Before reading anything: a key this node cannot read is a mistake rather than
	// an extension. See strict.go.
	if err := checkKeys(n, query, w.vars); err != nil {
		return nil, err
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

	err := setAttributes(n, obj, query.Get(LeafOf(n)), w)
	if err != nil && n.hasRequiredAttributes() {
		return nil, err
	}

	return obj, loadChildren(n, obj, query, w, claimed...)
}

// mergeInto folds one object's values and children into another, leaving what is
// already there alone: whoever wrote first declared it, and this is the second
// declaration arriving at the same key.
func mergeInto(into, from object.Object[object.Refrence]) {
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
			if v, ok := val.(object.Refrence); ok {
				into.Set(key, v)
			}
		}
	}
}

// claimedUnder is the set of keys this node's OWN attributes declare inside the child
// group named `key` — the second segment of every attribute path that starts there. The
// group must not read those as entries of its own.
func claimedUnder(n *Node, key string, vars Vars) map[string]bool {
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
		// And its variant spellings, where a block and an access group share a name: an
		// unclaimed `model/name_a` would read as an entry. Only where the second segment
		// is the last, since a suffix lands on the leaf.
		if len(a.Path) == 2 {
			for _, name := range VariesBy(a) {
				for _, value := range vars.Values(name) {
					if vp := variantPath(a.Path, value); vp != nil {
						if last, ok := vp[len(vp)-1].(string); ok {
							claim(last)
						}
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
func loadChildren(n *Node, obj object.Object[object.Refrence], query *yaseer.Query, w walk, claimed ...map[string]bool) error {
	list, _ := query.List()
	// A resource-container has two channels: directories are the resources it
	// contains, keys in its leaf document are its own nested data.
	hybrid := n.Group && n.Nested

	// A key an attribute declared is not an entry of the group sharing its name: an
	// agent's `model: {name: opus}` is the attribute's, not an access entry called
	// "name".
	var mine map[string]bool
	if len(claimed) > 0 {
		mine = claimed[0]
	}

	// Also keys nested inside the level's leaf document, so a group matches a map
	// authored as nested YAML as well as a directory. Filesystem entries win.
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
		cobj, err := load(itm, childQuery, w, claimedUnder(n, l, w.vars))
		if err != nil {
			return err
		}
		// Merge, never replace: two declarations can land at one key, and adding over
		// the top kept only whichever ran second.
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
				// Only a group keeps its fields in a leaf document; a nested resource reads its
				// own document root.
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
				// Filesystem entries win for the child that reads them. Under a hybrid node the
				// directory belongs to the resource group and the document key to the kind's own
				// group.
				if (inFS[l] && !(hybrid && itm.Nested)) || (n.Group && l == leaf) {
					continue
				}
				if err := processChild(itm, l, query.Get(leaf).Get(l)); err != nil {
					return err
				}
			}
		}
		if itm.Required {
			if err := requireEntries(itm, obj, query.Get(leaf)); err != nil {
				return err
			}
		}
	}

	return nil
}

func requireEntries(itm *Node, obj object.Object[object.Refrence], at *yaseer.Query) error {
	name := literalOf(itm.Match)
	group, err := obj.Child(name).Object()
	if err != nil {
		return errorWithLocation(at, "required group '%s'", name)
	}
	if len(group.Children()) == 0 {
		return errorWithLocation(at.Get(name), "required group '%s' is empty", name)
	}
	return nil
}
