package interp

import "github.com/taubyte/tau/pkg/tcc/engine"

// Generic schema-shape queries: the compiler selects passes from these
// predicates, and ContainerKeys keeps container keys declared only in the
// schema.

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

// UsesIndexing reports whether any group iterator declares an index footprint. It
// gates the forward chroot+IndexDriver passes and the decompile chroot-unwrap; the
// decompile subpackage consumes it, so it is exported.
func UsesIndexing(root *engine.Node) bool {
	return eachIter(root, func(iter *engine.Node) bool {
		_, ok := indexerOf(iter)
		return ok
	})
}

// ContainerKeys returns the config keys of every group whose instances are
// scopes (its iterator a group with children), in declaration order: the same
// test tcc-gen uses, so containers are declared only in the schema. Plural,
// since a lowering can synthesize a scope of another kind. The order is
// load-bearing, as for indexed groups.
func ContainerKeys(root *engine.Node) []string {
	var out []string
	for _, g := range root.Children {
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		if iter.Group && len(iter.Children) > 0 {
			if key, _ := g.Match.(string); key != "" {
				out = append(out, key)
			}
		}
	}
	return out
}

// ownGroups is the child groups a kind declares for itself, what Nested(...)
// attached. For a kind that is also a container it excludes the resource groups
// it holds, which the scope walk visits on its own.
func ownGroups(iter *engine.Node) []*engine.Node {
	if iter == nil || !iter.Nested {
		return nil
	}
	out := make([]*engine.Node, 0, len(iter.Children))
	for _, child := range iter.Children {
		if child.Nested {
			out = append(out, child)
		}
	}
	return out
}

// HeldResource tells a consumer walking a compiled object whether an entry filed
// under kind inside a resource is a resource the container holds, or an entry
// the resource declares for itself (an agent's tool).
func HeldResource(root *engine.Node, kind, key string) bool {
	if root == nil {
		return false
	}
	idField, _ := engine.IdentityOf(root)
	return heldUnder(root.Children, idField, kind, key)
}

// heldUnder answers the same question from the other side: walking the entries a kind
// declares for ITSELF at some key, which of them are actually resources the container
// holds at that key. It looks the resource group up by the key it shares.
func heldUnder(groups []*engine.Node, idField, key, entry string) bool {
	for _, g := range groups {
		if k, _ := g.Match.(string); k != key || len(g.Children) == 0 {
			continue
		}
		return keyedByIdentity(g.Children[0], idField, entry)
	}
	return false
}

// keyedByIdentity: a held resource is keyed by the id the schema names, which an
// entry never has, so the identity's validator tells them apart. A vocabulary
// with no validator on its identity merges nothing at one key either.
func keyedByIdentity(iter *engine.Node, idField, key string) bool {
	if iter == nil {
		return false
	}
	if _, ok := iter.Meta["resource"].([4]string); !ok {
		return true
	}
	for _, attr := range iter.Attributes {
		if attr.Name != idField || attr.Validator == nil {
			continue
		}
		return attr.Validator(key) == nil
	}
	return true
}
