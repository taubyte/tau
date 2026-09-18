// Package project reads a compiled project — the bytes the register holds — and
// answers the one question the runtime asks of it: what is the resource called
// this, and what does it say about itself.
//
// # It reads, and it is the only thing that does
//
//	a user writes the v2 schema
//	  -> tcc COMPILES it
//	    -> the compiled object is published to the register
//	      -> THIS reads it, per resource, on the request path
//
// A worker, a gateway, a proxy and `tau graph` all need that read, and a second
// implementation of it would be a second opinion about what a compiled project
// means. So it is here once and the consumers are thin.
//
// # Nothing here knows a kind
//
// There is no `Function`, no `Application`, no field called `memory`. A
// resource is the map tcc produced and this package hands it over with typed
// accessors, because the vocabulary is pkg/tcc/taubyte/v2/schema's and it
// changes there. A reader that mirrored the kinds would be a copy of the
// vocabulary that drifts silently — the failure would be a field that stopped
// arriving, not a compile error.
//
// # The shape it reads, which is tcc's and not this package's
//
// A compiled object is three trees:
//
//	object   the project itself: kinds at the root, namespaces nesting under
//	         `namespace/<id>`, resources keyed by id
//	indexes  a FLAT map, keyed by the whole path
//	         `projects/<pid>/<scope…>/<kind>/<name>/links`, whose value is the
//	         path into `object`
//	lowered  what the compiler produced rather than what was written; already
//	         folded into `object`, and not read here
//
// THE INDEX IS WHY THIS IS CHEAP. Every reference in v2 is by NAME — an edge
// says `cache/orders`, a route says which kind and which name — and a name has
// to become a resource without walking every scope's every group. tcc already
// built that table at compile time, so a lookup here is a map descent and not a
// search.
//
// # Scope resolves outward, and that is the vocabulary's rule
//
// A bare name is looked for in the scope that asked, then in each parent up to
// the project. That is how a namespace inherits its parent's resources without
// restating them, and it is the reason a lookup takes the ASKER's scope rather
// than a fully qualified path.
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

var (
	ErrNoObject  = errors.New("project: the compiled bytes carry no object")
	ErrNoID      = errors.New("project: the compiled object names no project")
	ErrNotFound  = errors.New("project: no such resource")
	ErrBadIndex  = errors.New("project: an index entry that does not name a resource")
	ErrWrongType = errors.New("project: the value is not of that type")
)

// segments of the compiled object and of an index value. They are tcc's, read
// here rather than guessed: see the package doc.
const (
	objectKey    = "object"
	indexesKey   = "indexes"
	projectsSeg  = "projects"
	branchesSeg  = "branches"
	linksSeg     = "links"
	namespaceSeg = "namespace"
)

// Project is a compiled project, ready to be read.
//
// IT IS IMMUTABLE AND SAFE TO SHARE. The register names objects by their
// content, so one loaded here is good forever and a caller is meant to hold it
// across requests rather than re-load it per call — which is the whole reason
// the register hands out a small pointer and a big object separately.
type Project struct {
	id   string
	name string

	root map[string]any

	// environments is the deployment table, read once. A compiled project is
	// immutable, so a consumer may ask per push without paying for the walk
	// per push.
	environmentsOnce sync.Once
	environments     []schema.Environment

	// located is the index read backwards: a resource id to the address a
	// caller would look it up by. A resolved reference and a reachable name
	// both arrive as ids, and both need the other direction.
	located map[string]where

	zonesOnce sync.Once
	zones     []Zone

	// hosts is memoized: a compiled project is immutable, so the walk that
	// produces it runs once however many requests ask.
	hostsOnce sync.Once
	hosts     []Host
	byHost    map[string][]Host

	// index is tcc's flat index with the trailing `links` segment taken off,
	// so a key is exactly `projects/<pid>/<scope…>/<kind>/<name>` and a lookup
	// is one map hit. Listings scan it, which is fine because a listing is a
	// walk of the project and never on the request path.
	index map[string][]string
}

// Load parses the bytes the register holds.
func Load(compiled []byte) (*Project, error) {
	var doc map[string]any
	if err := json.Unmarshal(compiled, &doc); err != nil {
		return nil, fmt.Errorf("project: reading the compiled object: %w", err)
	}

	root, _ := doc[objectKey].(map[string]any)
	if root == nil {
		return nil, ErrNoObject
	}
	id, _ := root["id"].(string)
	if id == "" {
		return nil, ErrNoID
	}
	name, _ := root["name"].(string)

	flat, _ := doc[indexesKey].(map[string]any)

	index := readIndex(flat)
	return &Project{id: id, name: name, root: root, index: index, located: locate(index, id)}, nil
}

// readIndex takes the trailing `links` off every key and keeps the values as
// strings. It runs once per object, and an object is content-addressed, so it
// runs once per deploy and never on a request.
func readIndex(flat map[string]any) map[string][]string {
	out := make(map[string][]string, len(flat))
	for key, v := range flat {
		path, ok := strings.CutSuffix(key, "/"+linksSeg)
		if !ok {
			continue
		}
		list, ok := v.([]any)
		if !ok {
			continue
		}
		links := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				links = append(links, s)
			}
		}
		if len(links) > 0 {
			out[path] = links
		}
	}
	return out
}

// ID is the project's id, which is what the index is keyed under.
func (p *Project) ID() string { return p.id }

// Name is what the author called it.
func (p *Project) Name() string { return p.name }

// Resource resolves a name the way the vocabulary does: in the scope that
// asked, then in each parent.
//
// scope is a chain of namespace NAMES from the project root — the `/shop/eu` an
// author writes, split — and nil is the project itself. The returned id is the
// resource's own, which is what an edge that already resolved carries.
func (p *Project) Resource(scope []string, kind, name string) (Resource, string, error) {
	if kind == "" || name == "" {
		return nil, "", fmt.Errorf("%w: %s/%s", ErrNotFound, kind, name)
	}

	cursor, err := p.scopeCursor(scope)
	if err != nil {
		return nil, "", err
	}

	// Outward: this scope, then each parent, by dropping the `namespace/<id>`
	// pair the level added.
	for {
		if r, id, err := p.at(cursor, kind, name); err == nil {
			return r, id, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, "", err
		}
		if len(cursor) < 2 {
			return nil, "", fmt.Errorf("%w: %s/%s", ErrNotFound, kind, name)
		}
		cursor = cursor[:len(cursor)-2]
	}
}

// Namespaces lists the namespace names directly under a scope.
func (p *Project) Namespaces(scope []string) ([]string, error) {
	return p.Names(scope, namespaceSeg)
}

// Kinds lists the kinds that have at least one resource in a scope, and Names
// lists the resources of one kind there. Neither resolves outward: they answer
// "what is declared HERE", which is what a walk of the project wants.
func (p *Project) Kinds(scope []string) ([]string, error) {
	cursor, err := p.scopeCursor(scope)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	for _, tail := range p.under(cursor, 2) {
		if kind := tail[0]; kind != namespaceSeg {
			seen[kind] = true
		}
	}

	out := make([]string, 0, len(seen))
	for kind := range seen {
		out = append(out, kind)
	}
	return out, nil
}

// Names lists the resources of one kind declared in a scope.
func (p *Project) Names(scope []string, kind string) ([]string, error) {
	cursor, err := p.scopeCursor(scope)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, tail := range p.under(append(cursor, kind), 1) {
		out = append(out, tail[0])
	}
	return out, nil
}

// under returns the tails of every index key that continues cursor by exactly
// depth more segments.
//
// THE EXACT DEPTH IS WHAT KEEPS A SCOPE ITS OWN. Every namespace under this one
// contributes keys that share this prefix, and a prefix match alone would list
// a child's resources as if they were declared here.
func (p *Project) under(cursor []string, depth int) [][]string {
	prefix := strings.Join(cursor, "/") + "/"

	var out [][]string
	for key := range p.index {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		if tail := strings.Split(rest, "/"); len(tail) == depth {
			out = append(out, tail)
		}
	}
	return out
}

// scopeCursor turns a chain of namespace names into the index path that scope
// sits at: `projects/<pid>/namespace/<id>/namespace/<id>…`.
//
// IT GOES THROUGH THE INDEX RATHER THAN THE OBJECT, because the object is keyed
// by id and the caller has names. That is exactly what the index is for, and
// doing it any other way would mean scanning every namespace for its `name`.
// scopeCursor turns a chain of scope NAMES into the index segments they are
// keyed by.
//
// A SCOPE IS NOT ALWAYS A NAMESPACE. Every level of the index is
// `<scope-kind>/<id>`, and the scope-kind is `namespace` only for a namespace:
// a kind that LOWERS becomes a container of its own, so a workflow's produced
// queues live under `workflow/<id>/queue/<name>`. Assuming the literal
// `namespace` here is what made every resource a lowering produced — every
// workflow step's queue and driver, every agent's loop and memory —
// unreachable from the read path while the compiler emitted them correctly.
func (p *Project) scopeCursor(scope []string) ([]string, error) {
	cursor := []string{projectsSeg, p.id}
	for _, name := range scope {
		kind, id, ok := p.scopeAt(cursor, name)
		if !ok {
			return nil, fmt.Errorf("%w: scope %q", ErrNotFound, strings.Join(scope, "/"))
		}
		if id == "" {
			return nil, fmt.Errorf("%w: scope %q", ErrBadIndex, name)
		}
		cursor = append(cursor, kind, id)
	}
	return cursor, nil
}

// scopeAt is the scope one name refers to at this cursor: which kind of
// container it is, and its id.
//
// THE NAMESPACE IS TRIED FIRST and the rest in a fixed order, so a project with
// a namespace and a workflow of one name resolves the same way on every node.
// The compiler refuses that collision; the order is here so that a compiled
// object which somehow carries one is not resolved by a map walk.
func (p *Project) scopeAt(cursor []string, name string) (kind, id string, ok bool) {
	for _, candidate := range scopeKinds() {
		links := p.index[strings.Join(append(append([]string{}, cursor...), candidate, name), "/")]
		if len(links) == 0 {
			continue
		}
		return candidate, lastSegment(links[len(links)-1]), true
	}
	return "", "", false
}

// scopeKinds is every kind that may be a scope, the namespace first.
//
// It is read off the vocabulary rather than listed here, so a kind that gains a
// lowering becomes addressable without a second edit — which is the failure
// this function exists because of.
var scopeKinds = sync.OnceValue(func() []string {
	out := []string{namespaceSeg}
	for _, kind := range schema.ScopeKindNames() {
		if kind != namespaceSeg {
			out = append(out, kind)
		}
	}
	return out
})

// Scopes are the names of every scope container directly under one scope,
// whatever KIND of container each is.
//
// IT IS NOT Namespaces. A walk of the whole project — what should this node be
// draining, what should it be firing — has to descend into a workflow's own
// scope as readily as into a namespace, and one that asked only for namespaces
// walked past every resource a lowering produced.
func (p *Project) Scopes(scope []string) ([]string, error) {
	cursor, err := p.scopeCursor(scope)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, kind := range scopeKinds() {
		for _, tail := range p.under(append(append([]string{}, cursor...), kind), 1) {
			out = append(out, tail[0])
		}
	}
	sort.Strings(out)
	return out, nil
}

// at reads one resource at exactly this scope, with no outward walk.
func (p *Project) at(cursor []string, kind, name string) (Resource, string, error) {
	links := p.index[strings.Join(append(append([]string{}, cursor...), kind, name), "/")]
	if len(links) == 0 {
		return nil, "", fmt.Errorf("%w: %s/%s", ErrNotFound, kind, name)
	}

	// The bucket accumulates one entry per publish and the current one is the
	// last written. Taking the first would serve whatever was published first
	// and never move again.
	target := links[len(links)-1]
	path, err := objectPath(target)
	if err != nil {
		return nil, "", err
	}

	r, ok := descend(p.root, path...).(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("%w: %s names nothing in the object", ErrBadIndex, target)
	}
	return Resource(r), path[len(path)-1], nil
}

// objectPath strips `branches/<branch>/projects/<pid>/` off an index value and
// returns what is left, which is a path into the object.
//
// THE BRANCH IS ONE SEGMENT WHATEVER IT IS. tcc percent-encodes it for exactly
// this reason (schema/index.go): `release/1.2` joined raw would turn one
// segment into two and every path after it would be off by one — silently, in
// the direction of the wrong resource.
func objectPath(target string) ([]string, error) {
	seg := strings.Split(target, "/")
	if len(seg) < 5 || seg[0] != branchesSeg || seg[2] != projectsSeg {
		return nil, fmt.Errorf("%w: %q", ErrBadIndex, target)
	}
	return seg[4:], nil
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

func descend(m map[string]any, path ...string) any {
	var cur any = m
	for _, step := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = next[step]
		if !ok {
			return nil
		}
	}
	return cur
}

// Resource is one compiled resource: whatever the vocabulary put in it.
//
// It is handed over as the map tcc produced rather than a struct per kind, for
// the reason in the package doc — and the accessors below exist so a consumer
// does not write the same type assertion twenty times, not so it can pretend
// the shape is known here.
type Resource map[string]any

// Has reports whether anything is at the path.
func (r Resource) Has(path ...string) bool { return descend(r, path...) != nil }

// String reads a string. A missing value and an empty one are the same answer,
// because a compiled resource omits what was not written.
func (r Resource) String(path ...string) string {
	s, _ := descend(r, path...).(string)
	return s
}

// Int reads a number.
//
// JSON HAS ONE NUMERIC TYPE, so every integer in a compiled object arrives as a
// float64 — a memory limit, a port, a concurrency. Converting it here is the
// alternative to twenty call sites each remembering that, and one of them
// forgetting.
func (r Resource) Int(path ...string) (int64, bool) {
	v, ok := descend(r, path...).(float64)
	if !ok {
		return 0, false
	}
	return int64(v), true
}

// Float reads a number that is not a whole one.
//
// IT IS NOT Int. A vocabulary Float — a retry factor, a similarity threshold —
// compiles to a JSON number, and reading one with Int truncates 1.5 to 1 while
// reading it with String answers nothing at all. Both are silent.
func (r Resource) Float(path ...string) (float64, bool) {
	v, ok := descend(r, path...).(float64)
	return v, ok
}

// Duration reads a duration. tcc compiles one to NANOSECONDS, so this is Int
// with the unit put back on — and the unit is stated here rather than assumed
// at four call sites that could each assume a different one.
func (r Resource) Duration(path ...string) (time.Duration, bool) {
	n, ok := r.Int(path...)
	if !ok {
		return 0, false
	}
	return time.Duration(n), true
}

// Bool reads a flag.
func (r Resource) Bool(path ...string) (bool, bool) {
	b, ok := descend(r, path...).(bool)
	return b, ok
}

// Strings reads a list of strings, e.g. an `ops` or an `egress` allowlist.
func (r Resource) Strings(path ...string) []string {
	list, ok := descend(r, path...).([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Sub is a nested block — `ports`, `variables`, an access block keyed by name.
func (r Resource) Sub(path ...string) Resource {
	m, _ := descend(r, path...).(map[string]any)
	return Resource(m)
}

// List is an ordered list of blocks, e.g. a gateway's transformers.
//
// IT IS NOT Sub. The vocabulary has both shapes and they are not
// interchangeable: `ports` is keyed by name and `transformers` is a sequence,
// because the order transformers run in is what they mean. Reading a list with
// Keys returns nothing and silently drops every entry — which for a
// fail-closed transformer is a security control that quietly stops existing.
// Map reads a block as it stands, for a consumer that wants the whole of one
// rather than a named field inside it.
//
// IT IS RAW ON PURPOSE. What is in a block is the vocabulary's, and a caller
// that hands one onwards — a worker giving a tier its serving settings — should
// not be re-typing every field a kind might grow.
func (r Resource) Map(path ...string) (map[string]any, bool) {
	m, ok := descend(r, path...).(map[string]any)
	return m, ok
}

func (r Resource) List(path ...string) []Resource {
	items, ok := descend(r, path...).([]any)
	if !ok {
		return nil
	}
	out := make([]Resource, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, Resource(m))
		}
	}
	return out
}

// Keys lists the names in a nested block, e.g. every port a workload declares.
func (r Resource) Keys(path ...string) []string {
	m := r.Sub(path...)
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
