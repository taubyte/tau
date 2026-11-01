package importer

import "strings"

// Source names allow what v2 names do not (`my-api`, `api/users/[id]`); a v2 name is a
// path segment, an environment-variable stem and a DNS label. A rename is always
// reported, or edges written against the original names point at nothing.

// Name is s as a v2 name, and whether it had to be changed.
//
// It is deterministic: the same input is the same name every time, so two runs
// of an import produce the same project and an edge written against one run
// resolves in the next.
func Name(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))

	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			// A leading digit is not a name; it is prefixed rather than
			// dropped, because dropping it makes `1api` and `api` the same
			// name and one of them then silently replaces the other.
			if i == 0 {
				b.WriteString("n")
			}
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	out := b.String()
	if out == "" {
		return "", false
	}
	return out, out != s
}

// Renamed is the finding a rename produces. It is here so every importer says
// it the same way and a corpus run can count them.
func Renamed(resource, key, from, to string) Finding {
	return Finding{
		Resource: resource,
		Key:      key,
		Level:    Approximated,
		Code:     "NAME_NOT_A_V2_NAME",
		Detail: "\"" + from + "\" is not a name this vocabulary accepts and was imported as \"" + to +
			"\"; anything outside this config that refers to it by the old name — a dashboard, a runbook, a script — still says the old one",
	}
}
