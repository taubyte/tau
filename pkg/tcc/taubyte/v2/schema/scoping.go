package schema

import "strings"

// WHERE A NAME IS LOOKED FOR, which is one rule and was four implementations.
//
// A resource reaches another by naming it, and says where to look with the namespace
// beside the name:
//
//	(absent)   its own scope, then each parent, then the root
//	/          the root
//	/billing   that scope, exactly
//	local         a scope BELOW its own, exactly
//
// The last line is the one every copy got wrong: `namespace: local` written in `shop` means
// `shop/local`, and a resolver that starts at `local` finds nothing — so the reference silently
// disappears from a graph, an audit, or both.
//
// Returned in the order they should be tried, so a caller stops at the first hit.
func scopeChain(own, ns string) []string {
	switch {
	case ns == "/":
		return []string{""}
	case strings.HasPrefix(ns, "/"):
		return []string{strings.TrimPrefix(ns, "/")}
	case ns != "":
		if own == "" {
			return []string{ns}
		}
		return []string{own + "/" + ns}
	}
	// No namespace: outward from where the reference was written.
	out := []string{own}
	for cur := own; cur != ""; {
		if cut := strings.LastIndex(cur, "/"); cut >= 0 {
			cur = cur[:cut]
		} else {
			cur = ""
		}
		out = append(out, cur)
	}
	return out
}
