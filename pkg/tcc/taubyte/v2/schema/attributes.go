package schema

import (
	"sort"
	"sync"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// attributes.go is how a consumer asks what a kind's fields are CALLED.
//
// # Why this exists
//
// A compiled resource is a tree of values at paths, and everything downstream of
// the compiler addresses those paths as strings: services/worker files a
// target's fields into a `Settings` map keyed by them, and every tier reads them
// back out. Until this, both ends spelled the path by hand — so they agreed with
// each other, and with the vocabulary, only by copying.
//
// That is not a hypothetical failure. Three keys had already drifted under a
// comment claiming they were "the vocabulary's own paths": `keys/ttl` and
// `keys/size` where the attributes are `ttl` and `size`, and `versioning` where
// it is `object/versioning`. Nothing failed, because the reader had copied the
// same invention. What an author wrote simply stopped taking effect.
//
// # What a compiled key IS, since three things look like one
//
//	Key("object","versioning")   the COMPILED key — "object/versioning". This.
//	InSection("keys")            a UI grouping. Not a path. This is what
//	                             "keys/ttl" was: a section name glued to a field.
//	Path("queue","consensus")    where the value is AUTHORED, which differs from
//	                             the compiled key wherever a matcher captures a
//	                             branch — `storage` authors `object:` and
//	                             compiles `type: object`.
//
// Attribute.Name is the first of those, already joined with "/" by the engine.
// Nothing here computes it; this only collects what the vocabulary declared.

// AttributeNames is every compiled key a resource of this kind may carry,
// sorted. An unknown kind has none rather than being an error: a caller asking
// about a kind that no longer exists wants an empty answer, which is what a
// check over it will then report.
func AttributeNames(kind string) []string {
	out := attributeIndex()[kind]
	return append([]string(nil), out...)
}

// IsAttribute reports whether a kind declares a field at this compiled key.
//
// IT IS THE WHOLE POINT OF THIS FILE. A consumer that files a value under a key
// can assert the key is one the vocabulary declares, and a rename then fails a
// test instead of quietly emptying a map.
func IsAttribute(kind, key string) bool {
	for _, name := range attributeIndex()[kind] {
		if name == key {
			return true
		}
	}
	return false
}

// attributeIndex walks the vocabulary once.
//
// IT WALKS THE SAME SHAPE `perenv_test` DOES — the kind's iter and every group
// nested under it — because a nested group's attributes are the resource's as
// much as the top-level ones are: `queue/retry/interval` is declared inside the
// sync type's block and is a field of a `sync`.
var attributeIndex = sync.OnceValue(func() map[string][]string {
	out := map[string][]string{}

	for _, g := range V2Resources {
		kind, _ := g.Match.(string)
		if kind == "" || len(g.Children) == 0 {
			continue
		}

		seen := map[string]bool{}
		var walk func(n *engine.Node)
		walk = func(n *engine.Node) {
			if n == nil {
				return
			}
			for _, a := range n.Attributes {
				if a != nil && a.Name != "" {
					seen[a.Name] = true
				}
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(g.Children[0])

		names := make([]string, 0, len(seen))
		for name := range seen {
			names = append(names, name)
		}
		sort.Strings(names)
		out[kind] = names
	}
	return out
})
