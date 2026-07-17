// Inlined utilities lifted from github.com/taubyte/tau/utils/{path,maps}.
// Copied so this package stands alone — only what yaseer actually uses
// (path Split/Join, maps Keys, maps SafeInterfaceToStringKeys).

package seer

import (
	"fmt"
	pathpkg "path"
)

// joinPath joins a string slice with the standard `/` separator.
// Wraps stdlib `path.Join` so call sites don't have to reach for the
// slices-as-args invocation.
func joinPath(names []string) string {
	return pathpkg.Join(names...)
}

// (splitPath was inlined from taubyte/tau but no caller in the
// trimmed-down package uses it — removed to keep utils.go honest.)

// mapKeys is the seer-private version of taubyte/tau utils/maps.Keys
// — returns every string key in the given map, in unspecified order.
func mapKeys[V any](m map[string]V) []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// safeInterfaceToStringKeys converts a map[interface{}]interface{} to a
// map[string]interface{}; non-string keys get fmt.Sprint'd. Mirrors
// taubyte/tau utils/maps.SafeInterfaceToStringKeys.
func safeInterfaceToStringKeys(m any) map[string]any {
	if m == nil {
		return nil
	}
	if m0, ok := m.(map[string]any); ok {
		return m0
	}
	m0, ok := m.(map[any]any)
	if !ok {
		return nil
	}
	r := make(map[string]any, len(m0))
	for k, v := range m0 {
		switch kk := k.(type) {
		case string:
			r[kk] = v
		default:
			r[fmt.Sprint(k)] = v
		}
	}
	return r
}
