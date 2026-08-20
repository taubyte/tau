package schema

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// WHAT THIS VOCABULARY PUTS IN THE INDEX.
//
// Every resource in v2 is reached by NAME. An edge says `kv/orders`, a route's handler
// says which kind and which name, and where to look is the namespace beside it. Nothing
// in a compiled resource carries the target's id, deliberately — a name survives a
// rename of nothing and an id survives a rename of everything, and the config is the
// author's document. So a node holding `(scope, kind, name)` has to turn it into a
// resource, and without a table that is a walk of every scope's every group.
//
// One footprint, therefore, and it is the answer to that question:
//
//	byName   projects/<pid>[/<scope>/<nsid>]…/<kind>/<name>/links
//	         -> branches/<b>/projects/<pid>[/<scope>/<nsid>]…/<kind>/<rid>
//
// Three things about the shape, each of which is a decision:
//
// THE KIND IS IN THE KEY. v1's by-name index has none — `projects/<pid>/<name>` is one
// bucket for every kind, and a consumer picks its own out by reading the resource type
// out of each value. That is a consumer parsing this vocabulary's address space, and v2
// has twenty-five kinds sharing one namespace where v1 had eight.
//
// THE SCOPE IS ITS ids, not its names. A namespace holds namespaces, so "where" is a
// chain either way; ids are what the compiled object is keyed by, so the key and the
// value it holds are built from the same segments. Resolving outward — a bare name is
// looked for here, then in each parent (scoping.go) — is dropping the last two segments
// and asking again, with no extra lookup to find the parent.
//
// THE VALUE CARRIES NO COMMIT, like v1's: the branch is in it, and resolving the branch
// to the release it is serving is the reader's, per-request. That is what the trailing
// `links` segment is for — the bucket accumulates one entry per publish and the reader
// picks the current one.

// index segments. Written here rather than imported from pkg/specs because those are
// v1's paths, and prefixing them was the only alternative to saying what v2's are.
const (
	projectsSegment = "projects"
	branchesSegment = "branches"
	linksSegment    = "links"
)

// scopeSegments turns the interpreter's scope — the ids from the project root down,
// joined — into path segments, each announced by the container's own key.
func scopeSegments(scope string) []string {
	if scope == "" {
		return nil
	}
	var out []string
	for _, id := range strings.Split(scope, "/") {
		out = append(out, scopeContainer, id)
	}
	return out
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

	value := append([]string{branchesSegment, ic.Branch, projectsSegment, ic.Project}, where...)
	value = append(value, ic.Kind, ic.ID)

	return []interp.IndexEntry{interp.Link(strings.Join(key, "/"), strings.Join(value, "/"))}, nil
}

// attachIndexes gives every kind the by-name footprint, the way attachAccess gives every
// kind its access groups: a rule that holds for all of them is not twenty-five lines
// each of which can be forgotten when the twenty-sixth is added.
//
// The POSITION is the declaration order of V2Resources, and the scope container comes
// last. Position decides the order buckets accumulate in, so it has to be something
// stated rather than whatever a map iteration produced; declaration order is the one
// order this vocabulary already has.
func attachIndexes() {
	for i, g := range V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		interp.Indexes(i, byName)(g.Children[0])
	}
}
