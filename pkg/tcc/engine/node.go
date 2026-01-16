package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// errorWithLocation formats an error message with file location information from a Query if available.
// It returns an error with location information formatted as: "message (file:line:column)" or just "message" if no location is available.
func errorWithLocation(query *yaseer.Query, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	filePath, line, column := query.Location()

	if filePath != "" {
		if line > 0 && column > 0 {
			return fmt.Errorf("%s:%d:%d: %s", filePath, line, column, msg)
		} else if line > 0 {
			return fmt.Errorf("%s:%d: %s", filePath, line, msg)
		}
		return fmt.Errorf("%s: %s", filePath, msg)
	}

	return fmt.Errorf("%s", msg)
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

	errStr := err.Error()
	// Check if the error already contains location information
	hasLocation := strings.Contains(errStr, "(at ")

	filePath, line, column := query.Location()

	// If error already has location info, just add context without duplicating location
	if hasLocation {
		return fmt.Errorf("%s: %w", context, err)
	}

	// Otherwise, add location information
	if filePath != "" {
		if line > 0 && column > 0 {
			return fmt.Errorf("%s:%d:%d: %s: %w", filePath, line, column, context, err)
		} else if line > 0 {
			return fmt.Errorf("%s:%d: %s: %w", filePath, line, context, err)
		}
		return fmt.Errorf("%s: %s: %w", filePath, context, err)
	}

	return fmt.Errorf("%s: %w", context, err)
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
		if attr.Key {
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
		if attr.Key {
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

func inferPathQuery(path []StringMatch, query *yaseer.Query) (*yaseer.Query, string, error) {
	query = query.Fork()
	var last_match string
	var cachedList []string
	var cacheValid bool

	for _, itm := range path {
		switch pitm := itm.(type) {
		case string:
			query.Get(pitm)
			last_match = pitm
			// Invalidate cache since query state changed
			cacheValid = false
		case StringMatcher:
			// Reuse cached list if available and query state hasn't changed
			var list []string
			var err error
			if cacheValid && cachedList != nil {
				list = cachedList
			} else {
				list, err = query.Fork().List()
				if err != nil {
					return nil, "", wrapErrorWithLocation(query, err, "list path matches failed")
				}
				// Cache the list for potential reuse (only valid until query state changes)
				cachedList = list
				cacheValid = true
			}

			var found bool
			for _, l := range list {
				if pitm.Match(l) {
					found = true
					query.Get(l)
					last_match = l
					// Invalidate cache since query state changed
					cacheValid = false
					break
				}
			}

			if !found {
				return nil, "", errorWithLocation(query, "can't find match for path")
			}
		}
	}

	return query, last_match, nil
}

func (n *Node) hasRequiredAttributes() bool {
	for _, attr := range n.Attributes {
		if attr.Required {
			return true
		}
	}
	return false
}

func getValue(aq *yaseer.Query, attr *Attribute) (val any, err error) {
	switch attr.Type {
	case TypeInt:
		var v int
		err = aq.Value(&v)
		if err != nil {
			err = wrapErrorWithLocation(aq, err, fmt.Sprintf("failed to get int value for attribute '%s'", attr.Name))
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
			err = wrapErrorWithLocation(aq, err, fmt.Sprintf("failed to get float value for attribute '%s'", attr.Name))
		}
		val = v
	case TypeString:
		var v string
		err = aq.Value(&v)
		if err != nil {
			err = wrapErrorWithLocation(aq, err, fmt.Sprintf("failed to get string value for attribute '%s'", attr.Name))
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

func setAttributes[T ObjectDataType](n *Node, obj object.Object[T], query *yaseer.Query) error {
	if len(n.Attributes) == 0 {
		return nil
	}

	for _, attr := range n.Attributes {
		if len(attr.Path) == 0 {
			attr.Path = []StringMatch{attr.Name}
		}
		aq, last_match, err := inferPathQuery(attr.Path, query)
		if err != nil {
			aq, last_match, err = inferPathQuery(attr.Compat, query)
			if err != nil {
				return wrapErrorWithLocation(query, err, fmt.Sprintf("attribute '%s' path resolution failed", attr.Name))
			}
		}

		if attr.Key {
			if len(last_match) > 0 {
				switch o := obj.(type) {
				case object.Object[object.Refrence]:
					o.Set(attr.Name, object.Refrence(last_match))
				}
				continue
			} else {
				return errorWithLocation(aq, "attribute %s has an empty key", attr.Name)
			}
		}

		val, err := getValue(aq, attr)
		if err != nil {
			aq, _, err = inferPathQuery(attr.Compat, query)
			if err != nil {
				return wrapErrorWithLocation(query, err, fmt.Sprintf("attribute '%s' compat path resolution failed", attr.Name))
			}
			val, err = getValue(aq, attr)
		}

		if err != nil {
			if attr.Required {
				// For required attributes, format as: filepath:line:column: required attribute 'name'
				// Don't include underlying YAML processing error details
				filePath, line, column := aq.Location()
				if filePath != "" {
					if line > 0 && column > 0 {
						return fmt.Errorf("%s:%d:%d: required attribute '%s'", filePath, line, column, attr.Name)
					} else if line > 0 {
						return fmt.Errorf("%s:%d: required attribute '%s'", filePath, line, attr.Name)
					}
					return fmt.Errorf("%s: required attribute '%s'", filePath, attr.Name)
				}
				return fmt.Errorf("required attribute '%s'", attr.Name)
			}
			if attr.Default != nil {
				switch o := obj.(type) {
				case object.Object[object.Refrence]:
					o.Set(attr.Name, attr.Default)
				}
			}
			continue
		}

		if attr.Validator != nil {
			if err = attr.Validator(val); err != nil {
				// For validation errors, format as: filepath:line:column: message
				// Don't include "validation failed for attribute" wrapper, just the location and validator error
				filePath, line, column := aq.Location()
				if filePath != "" {
					if line > 0 && column > 0 {
						return fmt.Errorf("%s:%d:%d: %w", filePath, line, column, err)
					} else if line > 0 {
						return fmt.Errorf("%s:%d: %w", filePath, line, err)
					}
					return fmt.Errorf("%s: %w", filePath, err)
				}
				return err
			}
		}

		switch o := obj.(type) {
		case object.Object[object.Refrence]:
			o.Set(attr.Name, val)
		}
	}

	return nil
}

func load[T ObjectDataType](n *Node, query *yaseer.Query) (object.Object[T], error) {
	obj := object.New[T]()

	if !n.Group {
		if err := setAttributes(n, obj, query); err != nil {
			return nil, err
		}
		return obj, nil
	}

	// file might or might not have config, so we ignore error
	err := setAttributes(n, obj, query.Fork().Get(NodeDefaultSeerLeaf))
	if err != nil && n.hasRequiredAttributes() {
		return nil, err
	}

	list, _ := query.Fork().List()
	for _, itm := range n.Children {
		for _, l := range list {
			if l == NodeDefaultSeerLeaf {
				continue
			}

			var (
				match   string
				matched bool
			)

			switch i := itm.Match.(type) {
			case string:
				if i == l {
					match = l
					matched = true
				}
			case StringMatcher:
				if i.Match(l) {
					match = l
					matched = true
				}
			}

			if !matched {
				continue
			}

			// Fork query for each child (Get() modifies query state)
			cobj, err := load[T](itm, query.Fork().Get(match))
			if err != nil {
				return nil, err
			}

			err = obj.Child(match).Add(cobj)
			if err != nil {
				return nil, err
			}
		}

	}

	return obj, nil
}
