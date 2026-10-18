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
//	         -> branches/<encoded-branch>/projects/<pid>[/<scope>/<nsid>]…/<kind>/<rid>
//
// Three things about the shape, each of which is a decision:
//
// THE KIND IS IN THE KEY. v1's by-name index has none — `projects/<pid>/<name>` is one
// bucket for every kind, and a consumer picks its own out by reading the resource type
// out of each value. That is a consumer parsing this vocabulary's address space, and v2
// has twenty-three kinds sharing one namespace where v1 had eight. (That number is not
// decoration — it is the whole reason this decision was taken, so it is pinned by a test
// rather than left to drift the next time a kind arrives or goes.)
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

// IndexAddress is the three fixed words an index key is written between.
type IndexAddress struct{ Projects, Branches, Links string }

// IndexShape is that address, for a consumer that has to decompose a key.
//
// EXPORTED FOR THE REASON EncodeBranch IS, and this is the half that was
// missing. The encoder was exported so "the two cannot drift apart" — and the
// words it writes between were not, so the one consumer that takes an index key
// apart kept verbatim copies of all three under a comment saying they were read
// here rather than guessed. They were guessed, correctly, for as long as nobody
// changed one.
func IndexShape() IndexAddress {
	return IndexAddress{Projects: projectsSegment, Branches: branchesSegment, Links: linksSegment}
}

// scopeSegments turns the interpreter's scope — the chain from the project root down —
// into path segments, each announced by the container's own key.
//
// EACH LEVEL SAYS WHAT IT IS. This wrote `namespace` in front of every id, which was true
// while a namespace was the only kind of scope there was; a scope a lowering makes is the
// SOURCE, in its container role, so its level is announced by the source's own kind.
// Announcing it as a namespace would key its resources under a path no reader could
// rebuild — `workflow/checkout` is indexed as `workflow/<name>`, so what is inside it has
// to be found under `workflow/<id>` too.
func scopeSegments(scope interp.ScopeChain) []string {
	var out []string
	for _, level := range scope {
		out = append(out, level.Kind, level.ID)
	}
	return out
}

// A BRANCH IS THE ONLY FREE-FORM SEGMENT IN THESE PATHS, and git lets it hold a `/`.
//
// Everything else a key or a value is built from is already a name something constrains:
// a kind is a group key, a name is `IsVariableName()`, an id and a project are CIDs. None
// of them can spell a `/`, so a `/` in those paths means one level down and nothing else.
// A branch obeys no such rule — `release/1.2` and `renovate/some-dep-1.2.3` are ordinary
// ref names — and joined raw it turns one segment into two. §4 of AGENTS.md is this same
// rule seen from the schema side: a key holding a `/` is a key pretending to be a path.
//
// The damage is not a mangled branch, it is a value that parses as NOTHING. A reader of
// this address space matches `branches/(?P<branch>[^/]+)/…projects/` (v1's, in
// pkg/specs/extract, is exactly that regex), and against `branches/release/1.2/projects/…`
// the whole match fails: no branch, no project, no resource, and no error either — an
// empty result that reads as "this path names nothing".
//
// SO THE BRANCH IS PERCENT-ENCODED, and only `/` and `%` are ever escaped:
//
//	main                 -> main
//	feature/x            -> feature%2Fx
//	release/1.2          -> release%2F1.2
//	fix/100%-coverage    -> fix%2F100%25-coverage
//
// Why this rather than the alternatives:
//
//   - A SUBSTITUTE CHARACTER — `release~1.2` — reads better and looks free, because `~`
//     is one of the handful git's ref rules forbid. But it is an encoding that only works
//     while its input is a valid ref, and the way it fails when it is not is silently, in
//     the direction of the wrong resource. Percent encoding is total: it round-trips any
//     string, whatever put it there.
//   - `%` MUST be escaped too, or the encoding is not reversible even in principle:
//     `a%2Fb` is itself a legal branch name (`git check-ref-format` accepts it), so
//     leaving `%` alone makes it the same segment as `a/b`.
//   - NOT `url.PathEscape`. It escapes far more than these two — a UTF-8 branch comes out
//     as `%C3%A9` noise — and which characters those are is the standard library's policy
//     rather than ours. What goes into a persisted index has to be frozen here.
//   - Hex or base64 over the whole branch round-trips as well and costs the legibility
//     that is the reason a register path is a path. Under this encoding the branches almost
//     every project has are untouched: `main` is `main`.
//
// The encoded form also needs nothing from the reader's grammar: `%2F` is not a `/`, so
// `[^/]+` takes the whole segment, and decoding it is one call rather than a new parser.

// branchEscape introduces an escape, and is therefore itself escaped.
const branchEscape = '%'

// EncodeBranch turns a branch into one path segment.
//
// Exported because this is only half a round trip: whatever resolves a value out of the
// index has to DecodeBranch what it reads there, and it lives beside the encoder so the
// two cannot drift apart.
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

// DecodeBranch reverses EncodeBranch.
//
// It is STRICT — `%` introduces `%2F` or `%25` and nothing else — because the encoded
// form is what the index is keyed by. A permissive decoder that read `%6Dain` as `main`
// would make that a second spelling of a branch, one no lookup ever finds; refusing it
// says so where it can be seen instead.
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

// attachIndexes gives every kind the by-name footprint, the way attachAccess gives every
// kind its access groups: a rule that holds for all of them is not twenty-three lines
// each of which can be forgotten when the twenty-fourth is added.
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
