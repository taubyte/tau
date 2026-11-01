package schema

import "strings"

// scopeChain is where a name is looked for, given the namespace beside it, in the
// order to try:
//
//	(absent)   its own scope, then each parent, then the root
//	/          the root
//	/billing   that scope, exactly
//	local      a scope below its own, exactly (`local` in `shop` is `shop/local`)
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
