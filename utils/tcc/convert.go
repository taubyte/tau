package tccUtils

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/object"
)

// MapToTCCObject converts a map to a TCC object.Object[object.Refrence]
// This is needed because TCC decompiler expects an object, but TNS returns a map
// The map structure from TNS matches the Flat() output structure
// Handles both map[string]any and map[any]any
func MapToTCCObject(m any) object.Object[object.Refrence] {
	// Normalize the map first
	normalized := normalizeMap(m)
	normalizedMap, ok := normalized.(map[string]any)
	if !ok {
		// If normalization failed, try to create empty object
		return object.New[object.Refrence]()
	}

	obj := object.New[object.Refrence]()

	for key, value := range normalizedMap {
		switch v := value.(type) {
		case map[string]any:
			// Recursively convert nested maps to child objects
			childObj := MapToTCCObject(v)
			sel := obj.Child(key)
			// If child exists, try to get it and merge, otherwise just add
			if sel.Exists() {
				// Try to get existing and merge data/children
				if existing, err := sel.Object(); err == nil {
					// Merge: copy data attributes and recursively merge children
					mergeObjectRecursive(existing, childObj)
				} else {
					// If we can't get existing, try to add (may fail if exists)
					sel.Add(childObj)
				}
			} else {
				sel.Add(childObj)
			}
		default:
			// For all other values (primitives, slices, etc.), store as data attribute
			obj.Set(key, object.Refrence(v))
		}
	}

	return obj
}

// normalizeMap converts map[any]any to map[string]any recursively
func normalizeMap(v any) any {
	switch val := v.(type) {
	case map[any]any:
		result := make(map[string]any)
		for k, v := range val {
			key, ok := k.(string)
			if !ok {
				key = fmt.Sprintf("%v", k)
			}
			result[key] = normalizeMap(v)
		}
		return result
	case map[string]any:
		result := make(map[string]any)
		for k, v := range val {
			result[k] = normalizeMap(v)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			result[i] = normalizeMap(v)
		}
		return result
	default:
		return v
	}
}

// mergeObjectRecursive merges data and children from src into dst recursively
func mergeObjectRecursive(dst, src object.Object[object.Refrence]) {
	// Copy data attributes - iterate through src's children to find data attributes
	// Note: We can't easily iterate just data attributes, so we check each child
	// to see if it's a data attribute (not a child object)
	for _, key := range src.Children() {
		srcChildSel := src.Child(key)
		if srcChildSel.Exists() {
			// This is a child object, merge recursively
			if srcChild, err := srcChildSel.Object(); err == nil {
				dstChildSel := dst.Child(key)
				if dstChildSel.Exists() {
					if dstChild, err := dstChildSel.Object(); err == nil {
						mergeObjectRecursive(dstChild, srcChild)
					}
				} else {
					dstChildSel.Add(srcChild)
				}
			}
		} else {
			// This might be a data attribute - try to get it
			val := src.Get(key)
			if val != nil {
				dst.Set(key, val)
			}
		}
	}
}
