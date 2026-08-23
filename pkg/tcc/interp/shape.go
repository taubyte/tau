package interp

import "github.com/taubyte/tau/pkg/tcc/engine"

// This file holds the generic "schema shape" queries the interpreter uses to stay
// schema-driven: it inspects the injected root's DSL annotations rather than
// restating v1's fixed shape. The compiler uses the usesX predicates to select
// which passes to run; ContainerKeys derives the nested-container config keys so
// "applications" is declared only in the schema, never here.

// eachIter calls fn for every group iterator reachable from root — each top-level
// group's iterator, and recursively the iterators of a container group's nested
// resource groups. fn returning true stops the walk early (the predicates below
// only need to know whether ANY iterator matches). Returns whether fn ever hit.
func eachIter(root *engine.Node, fn func(*engine.Node) bool) bool {
	return eachIterIn(root.Children, fn)
}

func eachIterIn(groups []*engine.Node, fn func(*engine.Node) bool) bool {
	for _, g := range groups {
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if fn(iter) {
			return true
		}
		// A container iterator (its own children are nested resource groups) may
		// carry ref/attach/index-annotated iterators too — recurse into it.
		if iter.Group && len(iter.Children) > 0 {
			if eachIterIn(iter.Children, fn) {
				return true
			}
		}
	}
	return false
}

// usesRefs reports whether any attribute in the schema carries a Ref(...)
// annotation — the signal that the ResolveRefs pass has work to do.
func usesRefs(root *engine.Node) bool {
	return eachIter(root, func(iter *engine.Node) bool {
		for _, a := range iter.Attributes {
			if _, ok := a.Meta["ref"]; ok {
				return true
			}
		}
		return false
	})
}

// usesAttachesToAll reports whether any group iterator is marked AttachesToAll —
// the signal that the AttachAll cross-cutting attachment pass is needed.
func usesAttachesToAll(root *engine.Node) bool {
	return eachIter(root, func(iter *engine.Node) bool {
		_, b := engine.AttachesFrom(iter)
		return b
	})
}

// UsesIndexing reports whether any group iterator declares an index footprint. It
// gates the forward chroot+IndexDriver passes and the decompile chroot-unwrap; the
// decompile subpackage consumes it, so it is exported.
func UsesIndexing(root *engine.Node) bool {
	return eachIter(root, func(iter *engine.Node) bool {
		_, ok := indexerOf(iter)
		return ok
	})
}

// ContainerKeys returns the config keys of every group whose instances are SCOPES — a
// top-level group whose iterator is itself a group with children and which carries no
// Resource descriptor — in the order the vocabulary declares them. Empty when the schema
// declares none. It is the same structural test the generator uses (tools/tcc-gen
// containerKey), kept in sync so "applications" is declared only in the schema, never
// restated here.
//
// PLURAL, AND THAT IS THE POINT. This answered with the FIRST such group, and everything
// downstream — reference resolution, the index, decompile, the lowering walk — was handed
// that one key and walked only it. One container was the whole truth while a scope was
// something an author writes; it stopped being true the moment a lowering could
// synthesize a scope of a DIFFERENT kind (interp.Lowerable). A vocabulary declaring a
// second one used to compile clean and then resolve no names inside it, which reads as
// the reference being wrong rather than as half the walk being missing.
//
// The order is the declaration order, and it is load-bearing for the same reason the
// order of indexed groups is: buckets accumulate across scopes as they are visited.
func ContainerKeys(root *engine.Node) []string {
	var out []string
	for _, g := range root.Children {
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if _, ok := iter.Meta["resource"].([4]string); ok {
			continue
		}
		if iter.Group && len(iter.Children) > 0 {
			if key, _ := g.Match.(string); key != "" {
				out = append(out, key)
			}
		}
	}
	return out
}
