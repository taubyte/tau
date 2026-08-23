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
// top-level group whose iterator is itself a group with children — in the order the vocabulary declares them. Empty when the schema
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
		if iter.Group && len(iter.Children) > 0 {
			if key, _ := g.Match.(string); key != "" {
				out = append(out, key)
			}
		}
	}
	return out
}

// ownGroups is the child groups a kind declares for ITSELF — what `Nested(...)` attached:
// its access blocks, its ports, its variables. For an ordinary resource that is all of
// them. For a kind that is ALSO a container it is the half that is not the resource groups
// it holds, and telling the two apart matters at every walk: what is nested is data
// belonging to the instance, and what is held is a resource the scope walk visits on its
// own. Walking the held ones as nested data ran every projection over them twice.
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

// HeldResource is the same question asked by a CONSUMER walking a compiled object: an
// entry filed under `kind` inside a resource is either something that resource declares
// for itself — an access entry, keyed by the label of what it reaches — or a resource the
// container HOLDS, when that kind is a container as well as a resource. Both land at the
// one key, and a consumer that guessed would report an agent's tool as a function it
// contains.
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

// keyedByIdentity answers, of one entry under a group key, whether it is a RESOURCE the
// scope holds rather than an entry the holder declares for itself.
//
// The two land at one key and nothing else separates them: a kind's name is what an
// access block is filed under and what a resource group is called, so a container that is
// also a resource has `function: {<produced id>: …, checkout: {ops: […]}}` — the loop it
// became beside the tool it reaches.
//
// THE IDENTITY IS THE WHOLE ANSWER, and it is not a new rule. A compiled resource is
// keyed by the id the schema names (engine.Identity, which is what RenameById keyed it
// by); an entry is keyed by a label the author chose and never had one. decompile's
// `strip` already states exactly this and relies on it — "an id is the one key nothing
// else in the tree is keyed by" — so this asks the schema's own validator for that field
// rather than inventing a second test.
//
// A vocabulary that puts no validator on its identity keeps the walk it had: nothing can
// be told apart, and nothing in such a schema merges two things at one key either.
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
