package gen

import (
	"github.com/taubyte/tau/pkg/tcc/interp"
	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// core renders the part of a spec that is not a field list. Its mechanism never
// changes with the vocabulary, but the words it addresses by (the object key, the
// index segments, which kinds are scopes) are baked in at generation, so a node
// needs no DSL at runtime to learn them.
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
