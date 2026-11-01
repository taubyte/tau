package schema

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// The one index footprint: every resource is reached by name, so a node holding
// (scope, kind, name) needs a table rather than a walk.
//
//	byName   projects/<pid>[/<scope>/<nsid>]…/<kind>/<name>/links
//	         -> branches/<encoded-branch>/projects/<pid>[/<scope>/<nsid>]…/<kind>/<rid>
//
// The kind is in the key, unlike v1's, so no consumer parses kinds out of values.
// The scope is its ids, so resolving outward drops two segments. The value carries
// no commit: `links` accumulates one entry per publish and the reader picks the
// current one.

// index segments. Written here rather than imported from pkg/specs because those are
// v1's paths, and prefixing them was the only alternative to saying what v2's are.
const (
	projectsSegment = "projects"
	branchesSegment = "branches"
	linksSegment    = "links"
)

// IndexAddress is the three fixed words an index key is written between.
type IndexAddress struct{ Projects, Branches, Links string }

// IndexShape is that address, for a consumer that has to decompose a key.
func IndexShape() IndexAddress {
	return IndexAddress{Projects: projectsSegment, Branches: branchesSegment, Links: linksSegment}
}

// scopeSegments turns the interpreter's scope chain into path segments, each
// level announced by its own container kind: what a lowering makes is keyed under
// `workflow/<id>`, not `namespace`.
func scopeSegments(scope interp.ScopeChain) []string {
	var out []string
	for _, level := range scope {
		out = append(out, level.Kind, level.ID)
	}
	return out
}

// A branch is the only free-form segment in these paths and git lets it hold `/`,
// which would split it and make readers matching `branches/([^/]+)/…projects/`
// fail silently. So it is percent-encoded, escaping only `/` and `%`:
//
//	main                 -> main
//	feature/x            -> feature%2Fx
//	release/1.2          -> release%2F1.2
//	fix/100%-coverage    -> fix%2F100%25-coverage
//
// `%` is escaped too, since `a%2Fb` is itself a legal branch. Not url.PathEscape:
// a persisted encoding must be frozen here, and common branches stay legible.

// branchEscape introduces an escape, and is therefore itself escaped.
const branchEscape = '%'

// EncodeBranch turns a branch into one path segment; DecodeBranch is its other
// half.
func EncodeBranch(branch string) string {
	if !strings.ContainsAny(branch, "/%") {
		return branch
	}
	// byte-wise deliberately: only these two ASCII bytes are escaped and every other
	// byte is copied through, so a multi-byte rune survives as the bytes it is made of.
	var out strings.Builder
	out.Grow(len(branch) + 4)
	for i := 0; i < len(branch); i++ {
		switch c := branch[i]; c {
		case '/', branchEscape:
			fmt.Fprintf(&out, "%%%02X", c)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// DecodeBranch reverses EncodeBranch. It is strict (`%2F` and `%25` only) so no
// second spelling of a branch, like `%6Dain`, can exist.
func DecodeBranch(segment string) (string, error) {
	if !strings.ContainsRune(segment, branchEscape) {
		return segment, nil
	}
	var out strings.Builder
	out.Grow(len(segment))
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c != branchEscape {
			out.WriteByte(c)
			continue
		}
		if i+2 >= len(segment) {
			return "", fmt.Errorf("decoding branch %q: %q is a truncated escape", segment, segment[i:])
		}
		switch escape := segment[i : i+3]; escape {
		case "%2F":
			out.WriteByte('/')
		case "%25":
			out.WriteByte(branchEscape)
		default:
			return "", fmt.Errorf("decoding branch %q: %q is not an escape this encoding produces — only %%2F and %%25, uppercase", segment, escape)
		}
		i += 2
	}
	return out.String(), nil
}

// byName: this resource, under what it is called, in the scope it is in.
func byName(ic interp.IndexCtx) ([]interp.IndexEntry, error) {
	if ic.Project == "" {
		return nil, fmt.Errorf("indexing %s/%s: the project has no id", ic.Kind, ic.ID)
	}
	if ic.Name == "" {
		return nil, fmt.Errorf("indexing %s/%s: it has no name", ic.Kind, ic.ID)
	}
	if ic.Branch == "" {
		return nil, fmt.Errorf("indexing %s/%s: the compile names no branch", ic.Kind, ic.Name)
	}

	where := scopeSegments(ic.Scope)
	key := append([]string{projectsSegment, ic.Project}, where...)
	key = append(key, ic.Kind, ic.Name, linksSegment)

	value := append([]string{branchesSegment, EncodeBranch(ic.Branch), projectsSegment, ic.Project}, where...)
	value = append(value, ic.Kind, ic.ID)

	return []interp.IndexEntry{interp.Link(strings.Join(key, "/"), strings.Join(value, "/"))}, nil
}

// attachIndexes gives every kind the by-name footprint. Position is V2Resources'
// declaration order with the scope container last, so bucket order is stated
// rather than map order.
func attachIndexes() {
	for i, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		interp.Indexes(i, byName)(g.Children[0])
	}
}
