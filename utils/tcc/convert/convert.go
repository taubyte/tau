// Package convert holds the map<->tcc object conversion. It lives in its own
// leaf package (depending only on pkg/tcc/object) so it can be imported from
// the GOOS=js wasm build without dragging in utils/tcc's network/libp2p
// siblings (publish.go, logs.go).
package convert

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
	case float64:
		// JSON numbers decode to float64, but tcc numeric fields are integers.
		// Small values map to int (what Int-attribute validators, e.g. replicas
		// min/max, type-switch on); large values that don't fit int32 (memory /
		// timeout in ns) map to int64, which the decompiler's timeout/memory
		// formatters accept — and which survives a 32-bit int target like
		// tinygo/wasm, where a plain int would overflow and drop the value.
		if val == float64(int64(val)) {
			i64 := int64(val)
			if int64(int32(i64)) == i64 {
				return int(i64)
			}
			return i64
		}
		return val
	case []any:
		result := make([]any, len(val))
		allStrings := len(val) > 0
		for i, v := range val {
			nv := normalizeMap(v)
			result[i] = nv
			if _, ok := nv.(string); !ok {
				allStrings = false
			}
		}
		// Restore []string from a JSON-eroded []any so the decompiler's
		// domains.([]string) assertions (pass2 websites/functions) hold. Serialized
		// sources (TNS maps, wasm JSON) lose the concrete slice type otherwise.
		if allStrings {
			strs := make([]string, len(result))
			for i, v := range result {
				strs[i] = v.(string)
			}
			return strs
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
