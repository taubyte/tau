package engine

import (
	"fmt"
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
		// Leaf node - write attributes
		return s.dumpAttributes(n, obj, query)
	}

	// Group node - handle config file and children
	if n.Match == nil {
		// Root node - write root attributes to config.yaml
		configQuery := query.Get(LeafOf(n)).Document()
		if err := s.dumpAttributes(n, obj, configQuery); err != nil {
			return err
		}
		if err := configQuery.Commit(); err != nil {
			return fmt.Errorf("committing config.yaml failed: %w", err)
		}
	}

	// Process children
	for _, child := range n.Children {
		if err := s.dumpChild(child, obj, query); err != nil {
			return err
		}
	}

	// Sync seer to filesystem
	return s.seer.Sync()
}

func (s *instance) dumpChild(child *Node, obj object.Object[object.Refrence], query *yaseer.Query) error {
	groupName, ok := child.Match.(string)
	if !ok {
		// Handle StringMatchAll or other matchers
		if _, ok := child.Match.(StringMatchAll); ok {
			// DefineIter or DefineIterGroup - check if it's a group
			if child.Group {
				// DefineIterGroup - used for applications
				return s.dumpApplications(child, obj, query)
			}
			// DefineIter - should not be called directly from dumpChild
			return fmt.Errorf("DefineIter should not be called directly from dumpChild")
		}
		return fmt.Errorf("unsupported child match type")
	}

	// Get the group object
	groupObj, err := obj.Child(groupName).Object()
	if err == object.ErrNotExist {
		return nil // No resources of this type
	}
	if err != nil {
		return fmt.Errorf("fetching %s failed: %w", groupName, err)
	}

	if child.Group {
		// DefineGroup - create directory structure
		if len(child.Children) == 0 {
			return nil
		}

		// Get the DefineIter node
		iterNode := child.Children[0]

		// Check if this is a DefineIterGroup (e.g., applications)
		// DefineIterGroup has StringMatchAll and Group=true
		if _, isIterGroup := iterNode.Match.(StringMatchAll); isIterGroup && iterNode.Group {
			// This is a group like "applications" with nested IterGroup
			return s.dumpApplications(iterNode, groupObj, query.Get(groupName))
		}

		// Regular DefineIter - write each resource as {groupName}/{name}.yaml
		for _, name := range groupObj.Children() {
			resObj, err := groupObj.Child(name).Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed: %w", groupName, name, err)
			}

			// Create document: {groupName}/{name}.yaml
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
		// Single resource - should not happen for groups
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
		for _, name := range groupObj.Children() {
			entry, err := groupObj.Child(name).Object()
			if err != nil {
				return fmt.Errorf("fetching %s/%s failed: %w", groupName, name, err)
			}
			if err := s.dumpAttributes(iter, entry, query.Get(groupName).Get(name)); err != nil {
				return fmt.Errorf("dumping %s/%s failed: %w", groupName, name, err)
			}
		}
	}
	return nil
}

// dumpApplications writes applications to the filesystem.
// appsObj is the applications object (keyed by name), query points to the applications directory.
func (s *instance) dumpApplications(iterGroupNode *Node, appsObj object.Object[object.Refrence], query *yaseer.Query) error {
	// Get resource nodes (TaubyteRessources)
	resourceNodes := iterGroupNode.Children

	for _, appName := range appsObj.Children() {
		appObj, err := appsObj.Child(appName).Object()
		if err != nil {
			return fmt.Errorf("fetching scope %s failed: %w", appName, err)
		}

		// Write application config.yaml: {appName}/config.yaml
		appConfigQuery := query.Get(appName).Get(LeafOf(iterGroupNode)).Document()
		if err := s.dumpAttributes(iterGroupNode, appObj, appConfigQuery); err != nil {
			return fmt.Errorf("dumping scope %s failed: %w", appName, err)
		}
		if err := appConfigQuery.Commit(); err != nil {
			return fmt.Errorf("committing application %s config failed: %w", appName, err)
		}

		// Write application resources
		for _, resourceNode := range resourceNodes {
			if err := s.dumpChild(resourceNode, appObj, query.Get(appName)); err != nil {
				return fmt.Errorf("dumping application %s resources failed: %w", appName, err)
			}
		}
	}

	return nil
}

func (s *instance) dumpAttributes(n *Node, obj object.Object[object.Refrence], query *yaseer.Query) error {
	for _, attr := range n.Attributes {
		val := obj.Get(attr.Name)
		if val == nil {
			// Check if required
			if attr.Required {
				return fmt.Errorf("required attribute '%s' is missing", attr.Name)
			}
			// Skip nil values
			continue
		}

		// Skip values that equal the default (no need to write them)
		if attr.Default != nil && val == attr.Default {
			continue
		}

		// Validate value using schema validator if present
		if attr.Validator != nil {
			if err := attr.Validator(val); err != nil {
				return fmt.Errorf("attribute '%s' validation failed: %w", attr.Name, err)
			}
		}

		// Build path from Path() definition
		path := attr.Path
		if len(path) == 0 {
			path = []StringMatch{attr.Name}
		}

		// Write value to path
		if err := writeValueToPath(n, query, path, attr, val, obj); err != nil {
			return fmt.Errorf("writing attribute '%s' failed: %w", attr.Name, err)
		}
	}

	return nil
}

func writeValueToPath(n *Node, query *yaseer.Query, path []StringMatch, attr *Attribute, val interface{}, obj object.Object[object.Refrence]) error {
	q := query

	for depth, p := range path {
		switch pt := p.(type) {
		case string:
			q = q.Get(pt)
		case StringMatcher:
			// Handle Either() matcher
			matcherStr := pt.String()
			if strings.HasPrefix(matcherStr, "Either(") && strings.HasSuffix(matcherStr, ")") {
				// Parse "Either([value1 value2])" format
				valuesStr := strings.TrimPrefix(strings.TrimSuffix(matcherStr, ")"), "Either(")
				valuesStr = strings.Trim(valuesStr, "[]")
				values := strings.Fields(valuesStr)

				// For Key() attributes, use the stored key value to determine Either() branch
				if attr.Key {
					keyVal, err := obj.GetString(attr.Name)
					if err != nil {
						return fmt.Errorf("key attribute '%s' is not a string", attr.Name)
					}
					// Validate and use the key value
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
					// For a non-Key Either(), the branch is whatever the Key()
					// attribute on this same switch captured. Which attribute that is
					// comes from the SCHEMA — the sibling whose path has the same
					// Either at the same depth and carries Key() — not from a field
					// conventionally called `type`. v1 named every switch's key `type`
					// and this looked for exactly that, so a vocabulary that names it
					// `engine/kind` could not be written back at all.
					typeVal, err := branchKeyValue(n, obj, path, depth)
					if err != nil {
						return fmt.Errorf("cannot determine Either() branch for attribute '%s': %w", attr.Name, err)
					}
					// Find matching option
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

	// Set the final value
	if err := q.Set(val).Commit(); err != nil {
		return fmt.Errorf("setting value failed: %w", err)
	}
	return nil
}

// branchKeyValue reads which branch an Either() switch took, off the attribute that
// captured it: the Key() attribute whose path holds an Either at the same depth. That
// is the same construct the loader uses to record the branch, so the write-back asks
// the same question the read did.
func branchKeyValue(n *Node, obj object.Object[object.Refrence], path []StringMatch, depth int) (string, error) {
	for _, a := range n.Attributes {
		if !a.Key || len(a.Path) <= depth {
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
		v, err := obj.GetString(a.Name)
		if err != nil || v == "" {
			continue
		}
		return v, nil
	}
	// No fallback. There WAS one — a field literally called `type`, which is one
	// vocabulary's word in the layer that must not have one, and on a live path: any
	// other vocabulary with a `type` attribute meaning something else would have taken
	// the wrong branch on write-back, silently. Key() is how a schema says which
	// attribute decides a switch, and a schema that does not say cannot be written back.
	return "", fmt.Errorf("no Key() attribute captured this switch")
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
