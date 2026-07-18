package interp

import "github.com/taubyte/tau/pkg/tcc/engine"

// This file holds the generic "schema shape" queries the interpreter uses to stay
// schema-driven: it inspects the injected root's DSL annotations rather than
// restating v1's fixed shape. The compiler uses the usesX predicates to select
// which passes to run; containerKey derives the nested-container config key so
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
		b, _ := iter.Meta["attachesToAll"].(bool)
		return b
	})
}

// indexMetaKeys are the group-iterator annotations that give a group an index
// footprint; any one present means the IndexDriver (and the chroot that makes room
// for its `indexes` sibling) is needed.
var indexMetaKeys = []string{
	"indexByName",
	"indexForeignKey",
	"indexRepo",
	"indexName",
	"indexByScope",
	"indexPlaceholder",
}

// UsesIndexing reports whether any group iterator declares an index footprint. It
// gates the forward chroot+IndexDriver passes and the decompile chroot-unwrap; the
// decompile subpackage consumes it, so it is exported.
func UsesIndexing(root *engine.Node) bool {
	return eachIter(root, func(iter *engine.Node) bool {
		for _, k := range indexMetaKeys {
			if _, ok := iter.Meta[k]; ok {
				return true
			}
		}
		return false
	})
}

// containerKey returns the config key of the nested container group — the top-level
// group whose iterator is itself a group with children and carries no Resource
// descriptor (the applications container) — or "" if the schema declares none. This
// is the same structural test the generator uses (tools/tcc-gen containerKey), kept
// in sync so "applications" is declared only in the schema, never restated here.
func containerKey(root *engine.Node) string {
	for _, g := range root.Children {
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if _, ok := iter.Meta["resource"].([4]string); ok {
			continue
		}
		if iter.Group && len(iter.Children) > 0 {
			key, _ := g.Match.(string)
			return key
		}
	}
	return ""
}
