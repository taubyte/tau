package gen

import (
	"github.com/taubyte/tau/pkg/tcc/interp"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// core.go renders the part of a spec that is not a field list.
//
// # Why this is generated when its body never changes with the vocabulary
//
// Most of it is mechanism: descending a map at a path, reading an index,
// walking outward from a scope. None of that moves when a kind is added.
//
// What DOES move is every word it addresses by — the key a compiled project
// sits under, the three words an index key is written between, which kinds are
// scopes, what an access entry calls its ops. Those were read at runtime from
// the compiler, which made a node link the DSL to learn four strings, or worse
// were spelled by hand and drifted. Baking them at generation is what makes a
// generated spec describe the vocabulary it was generated from, and a stale one
// obvious rather than silently wrong.
type core struct {
	Package string

	// The compiled envelope, which tcc calls "a contract and not a convention".
	ObjectKey  string
	IndexesKey string

	// The three words an index key is written between.
	ProjectsSeg string
	BranchesSeg string
	LinksSeg    string

	// NamespaceSeg is the one container that produces nothing, which is how a
	// lowered scope is told from an authored one.
	NamespaceSeg string

	// ScopeKinds is every kind that may be a scope, the namespace first.
	ScopeKinds []string

	// ZoneKind is the kind a cloud is authoritative for names under.
	ZoneKind string
}

// RenderCore emits the reader core for a package.
func RenderCore(pkg string) ([]byte, error) {
	shape := v2.IndexShape()

	const namespace = "namespace"
	kinds := []string{namespace}
	for _, kind := range v2.ScopeKindNames() {
		if kind != namespace {
			kinds = append(kinds, kind)
		}
	}

	return exec("project.go.tmpl", core{
		Package:      pkg,
		ObjectKey:    interp.ProjectKey,
		IndexesKey:   interp.IndexesKey,
		ProjectsSeg:  shape.Projects,
		BranchesSeg:  shape.Branches,
		LinksSeg:     shape.Links,
		NamespaceSeg: namespace,
		ScopeKinds:   kinds,
		ZoneKind:     "zone",
	}, "specs core")
}
