package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// Dump writes an object back to the filesystem using the engine's schema and seer.
// This is the reverse of Process() - it uses the schema's Path() definitions to write
// attributes back to their YAML locations.
func (s *instance) Dump(obj object.Object[object.Refrence]) error {
	query := s.seer.Query()
	n := s.schema.root
	if !n.Group {
		return s.dumpAttributes(n, obj, query)
	}

	if n.Match == nil {
		configQuery := query.Get(LeafOf(n)).Document()
		if err := s.dumpAttributes(n, obj, configQuery); err != nil {
			return err
		}
		for _, child := range n.Children {
			if err := s.dumpTable(child, obj, configQuery); err != nil {
				return err
			}
		}
		if err := configQuery.Commit(); err != nil {
			return fmt.Errorf("committing config.yaml failed: %w", err)
		}
	}

	for _, child := range n.Children {
		if n.Match == nil && s.isTable(child, obj) {
			continue
		}
		if err := s.dumpChild(child, obj, query, false); err != nil {
			return err
		}
	}

	return s.seer.Sync()
}

// heldResource: a kind that is both resource and container has group keys
// holding its own entries and the resources it holds. A resource carries the
// schema's identity; an entry never does.
func (s *instance) heldResource(entry object.Object[object.Refrence]) bool {
	idField, _ := IdentityOf(s.schema.root)
	return idField != "" && entry.Get(idField) != nil
}

// isTable reports a root group whose entries carry no identity: a table the root
// document holds, not resources each kept in a document of their own.
func (s *instance) isTable(child *Node, obj object.Object[object.Refrence]) bool {
	if idField, _ := IdentityOf(s.schema.root); idField == "" {
		return false
	}
	name, ok := child.Match.(string)
	if !ok || !child.Group || len(child.Children) != 1 || child.Children[0].Group {
		return false
	}
	group, err := obj.Child(name).Object()
	if err != nil {
		return false
	}
	entries := group.Children()
	for _, entry := range entries {
		e, err := group.Child(entry).Object()
		if err != nil || s.heldResource(e) {
			return false
		}
	}
	return len(entries) > 0
}

func (s *instance) dumpTable(child *Node, obj object.Object[object.Refrence], query *yaseer.Query) error {
	if !s.isTable(child, obj) {
		return nil
	}
	name := child.Match.(string)
	iter := child.Children[0]
	group, err := obj.Child(name).Object()
	if err != nil {
		return err
	}
	for _, entry := range group.Children() {
		e, err := group.Child(entry).Object()
		if err != nil {
			return err
		}
		at := query.Get(name).Get(entry)
		if err := s.dumpAttributes(iter, e, at); err != nil {
			return fmt.Errorf("dumping %s/%s failed: %w", name, entry, err)
		}
		if err := s.dumpNested(iter, e, at); err != nil {
			return fmt.Errorf("dumping nested groups for %s/%s failed: %w", name, entry, err)
		}
	}
	return nil
}

func (s *instance) dumpChild(child *Node, obj object.Object[object.Refrence], query *yaseer.Query, mixed bool) error {
	groupName, ok := child.Match.(string)
	if !ok {
		if _, ok := child.Match.(StringMatchAll); ok {
			if child.Group {
				return s.dumpApplications(child, obj, query)
			}
			return fmt.Errorf("DefineIter should not be called directly from dumpChild")
		}
		return fmt.Errorf("unsupported child match type")
	}

	groupObj, err := obj.Child(groupName).Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("fetching %s failed: %w", groupName, err)
	}

	if child.Group {
		if len(child.Children) == 0 {
			return nil
		}

		iterNode := child.Children[0]

		if _, isIterGroup := iterNode.Match.(StringMatchAll); isIterGroup && iterNode.Group {
			return s.dumpApplications(iterNode, groupObj, query.Get(groupName))
		}

		for _, name := range groupObj.Children() {
			resObj, err := groupObj.Child(name).Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed: %w", groupName, name, err)
			}
			if mixed && !s.heldResource(resObj) {
				continue // an entry the holder declares for itself; dumpNested writes it
			}

			docQuery := query.Get(groupName).Get(name).Document()
			if err := s.dumpAttributes(iterNode, resObj, docQuery); err != nil {
				return fmt.Errorf("dumping attributes for %s/%s failed: %w", groupName, name, err)
			}
			// A nested resource's own groups are written into the SAME document,
			// which is where they were read from. Without this the dump is lossy:
			// the resource round-trips minus everything it declared.
			if err := s.dumpNested(iterNode, resObj, docQuery); err != nil {
				return fmt.Errorf("dumping nested groups for %s/%s failed: %w", groupName, name, err)
			}
			if err := docQuery.Commit(); err != nil {
				return fmt.Errorf("committing %s/%s failed: %w", groupName, name, err)
			}
		}
	} else {
		return fmt.Errorf("non-group child in group context")
	}

	return nil
}

// dumpNested writes a nested resource's child groups back into the resource's own
// document — the inverse of load's nested branch. Each child group is a map keyed by
// the entry name, holding that entry's attributes.
func (s *instance) dumpNested(n *Node, obj object.Object[object.Refrence], query *yaseer.Query) error {
	if !n.Nested {
		return nil
	}
	for _, child := range n.Children {
		if !child.Nested {
			continue
		}
		groupName, ok := child.Match.(string)
		if !ok || len(child.Children) == 0 {
			continue
		}
		// A list is one ordered value, not children — write it as the sequence it
		// was authored as. Setting the whole slice keeps the order and lets yaml
		// emit a real sequence, which Get(index) would not.
		if child.Sequence {
			if items, exists := obj.Get(groupName).([]any); exists && len(items) > 0 {
				if err := query.Get(groupName).Set(items).Commit(); err != nil {
					return fmt.Errorf("dumping %s failed: %w", groupName, err)
				}
			}
			continue
		}
		groupObj, err := obj.Child(groupName).Object()
		if err == object.ErrNotExist {
			continue
		}
		if err != nil {
			return fmt.Errorf("fetching %s failed: %w", groupName, err)
		}

		iter := child.Children[0]
		mixed := n.Group && n.Nested
		for _, name := range groupObj.Children() {
			entry, err := groupObj.Child(name).Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed: %w", groupName, name, err)
			}
			if mixed && s.heldResource(entry) {
				continue // a resource the container holds; dumpChild writes its own document
			}
			if err := s.dumpAttributes(iter, entry, query.Get(groupName).Get(name)); err != nil {
				return fmt.Errorf("dumping %s/%s failed: %w", groupName, name, err)
			}
		}
	}
	return nil
}

// dumpApplications writes appsObj, keyed by name, under query.
func (s *instance) dumpApplications(iterGroupNode *Node, appsObj object.Object[object.Refrence], query *yaseer.Query) error {
	resourceNodes := iterGroupNode.Children

	for _, appName := range appsObj.Children() {
		appObj, err := appsObj.Child(appName).Object()
		if err != nil {
			return fmt.Errorf("fetching scope %s failed: %w", appName, err)
		}

		appConfigQuery := query.Get(appName).Get(LeafOf(iterGroupNode)).Document()
		if err := s.dumpAttributes(iterGroupNode, appObj, appConfigQuery); err != nil {
			return fmt.Errorf("dumping scope %s failed: %w", appName, err)
		}
		// A container may also be a resource. Its own nested groups belong in the
		// same leaf document as its attributes; only the non-nested child groups are
		// directories holding resources inside it.
		if err := s.dumpNested(iterGroupNode, appObj, appConfigQuery); err != nil {
			return fmt.Errorf("dumping scope %s nested data failed: %w", appName, err)
		}
		if err := appConfigQuery.Commit(); err != nil {
			return fmt.Errorf("committing application %s config failed: %w", appName, err)
		}

		for _, resourceNode := range resourceNodes {
			if resourceNode.Nested {
				continue
			}
			if err := s.dumpChild(resourceNode, appObj, query.Get(appName), iterGroupNode.Group && iterGroupNode.Nested); err != nil {
				return fmt.Errorf("dumping application %s resources failed: %w", appName, err)
			}
		}
	}

	return nil
}

func (s *instance) dumpAttributes(n *Node, obj object.Object[object.Refrence], query *yaseer.Query) error {
	for _, attr := range n.Attributes {
		val := read(obj, attr)
		if val == nil {
			if attr.Required {
				return fmt.Errorf("required attribute '%s' is missing", attr.Name)
			}
			continue
		}

		if attr.Default != nil && val == attr.Default && defaultApplies(n, attr, obj) {
			continue
		}

		if attr.Validator != nil {
			if err := attr.Validator(val); err != nil {
				return fmt.Errorf("attribute '%s' validation failed: %w", attr.Name, err)
			}
		}

		path := attr.Path
		if len(path) == 0 {
			path = []StringMatch{attr.Name}
		}

		if err := writeValueToPath(n, query, path, attr, val, obj); err != nil {
			return fmt.Errorf("writing attribute '%s' failed: %w", attr.Name, err)
		}
	}

	return nil
}

// defaultApplies is whether a value equal to attr's default may be left out of the
// document. A default scoped to a branch is filled in again only where that branch is
// taken, so anywhere else the value is the author's and has to be written.
func defaultApplies(n *Node, attr *Attribute, obj object.Object[object.Refrence]) bool {
	c, conditional := attr.Meta["defaultWhen"].(ConditionSpec)
	if !conditional {
		return true
	}
	for _, sib := range n.Attributes {
		if sib.Name == c.Field {
			v, _ := read(obj, sib).(string)
			return slices.Contains(c.In, v)
		}
	}
	return false
}

// read pulls an attribute's compiled value back out, at the key it was written to —
// which for a multi-segment key is a nested level rather than a flat leaf.
func read(obj object.Object[object.Refrence], attr *Attribute) any {
	segments := CompiledSegments(attr)
	last := len(segments) - 1
	if last == 0 {
		return obj.Get(segments[0])
	}
	at, err := obj.Fetch(segments[:last]...)
	if err != nil {
		return nil
	}
	return at.Get(segments[last])
}

func readString(obj object.Object[object.Refrence], attr *Attribute) (string, error) {
	switch v := read(obj, attr).(type) {
	case string:
		return v, nil
	case fmt.Stringer:
		return v.String(), nil
	}
	return "", fmt.Errorf("attribute '%s' is not a string", attr.Name)
}

func writeValueToPath(n *Node, query *yaseer.Query, path []StringMatch, attr *Attribute, val interface{}, obj object.Object[object.Refrence]) error {
	q := query

	for depth, p := range path {
		switch pt := p.(type) {
		case string:
			q = q.Get(pt)
		case StringMatcher:
			matcherStr := pt.String()
			if strings.HasPrefix(matcherStr, "Either(") && strings.HasSuffix(matcherStr, ")") {
				// Parse "Either([value1 value2])" format
				valuesStr := strings.TrimPrefix(strings.TrimSuffix(matcherStr, ")"), "Either(")
				valuesStr = strings.Trim(valuesStr, "[]")
				values := strings.Fields(valuesStr)

				if attr.Captures {
					keyVal, err := readString(obj, attr)
					if err != nil {
						return fmt.Errorf("key attribute '%s' is not a string", attr.Name)
					}
					matched := false
					for _, option := range values {
						if keyVal == option {
							q = q.Get(keyVal)
							matched = true
							break
						}
					}
					if !matched {
						return fmt.Errorf("key attribute '%s' value '%s' does not match Either() options %v", attr.Name, keyVal, values)
					}
				} else {
					// For a non-Key Either(), the branch is what the switch's Captures() attribute
					// captured, found from the schema.
					typeVal, err := branchKeyValue(n, obj, path, depth)
					if err != nil {
						return fmt.Errorf("cannot determine Either() branch for attribute '%s': %w", attr.Name, err)
					}
					matched := false
					for _, option := range values {
						if typeVal == option {
							q = q.Get(option)
							matched = true
							break
						}
					}
					if !matched {
						return fmt.Errorf("type value '%s' does not match Either() options %v for attribute '%s'", typeVal, values, attr.Name)
					}
				}
			} else {
				return fmt.Errorf("unsupported StringMatcher '%s'", matcherStr)
			}
		default:
			return fmt.Errorf("unsupported path type %T", p)
		}
	}

	// A captured value is the key: writing it back would write the key's name into
	// it. Reaching the branch is the write.
	if attr.Captures {
		return nil
	}

	if err := q.Set(val).Commit(); err != nil {
		return fmt.Errorf("setting value failed: %w", err)
	}
	return nil
}

// branchKeyValue reads which branch an Either() switch took, off the attribute that
// captured it: the Captures() attribute whose path holds an Either at the same depth. That
// is the same construct the loader uses to record the branch, so the write-back asks
// the same question the read did.
func branchKeyValue(n *Node, obj object.Object[object.Refrence], path []StringMatch, depth int) (string, error) {
	for _, a := range n.Attributes {
		if !a.Captures || len(a.Path) <= depth {
			continue
		}
		if _, isEither := a.Path[depth].(FiniteMatcher); !isEither {
			continue
		}
		// The SAME switch, not merely one at the same depth: a proxy has two — `host`
		// and `forward` — and matching on depth alone read the host's branch into the
		// forward's path.
		if !samePrefix(a.Path, path, depth) {
			continue
		}
		v, err := readString(obj, a)
		if err != nil || v == "" {
			continue
		}
		return v, nil
	}
	// No fallback to a field called `type`: Captures() is how a schema says which
	// attribute decides a switch.
	return "", fmt.Errorf("no Captures() attribute captured this switch")
}

// samePrefix reports whether two attribute paths name the same block up to depth.
func samePrefix(a, b []StringMatch, depth int) bool {
	for i := 0; i < depth; i++ {
		x, okx := a[i].(string)
		y, oky := b[i].(string)
		if !okx || !oky || x != y {
			return false
		}
	}
	return true
}
