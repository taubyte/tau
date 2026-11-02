package schema

import (
	"sort"
	"sync"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// attributes.go is how a consumer asks what a kind's fields are called, so code
// filing values under compiled keys asserts them instead of copying the
// spelling. A compiled key is Key(...)'s joined form, not an InSection name and
// not the authored Path.

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
		// at is the key a block's fields are filed under, and a group's or a list's
		// entries inside one are `*`, the segment a read computes.
		var walk func(n *engine.Node, at string)
		walk = func(n *engine.Node, at string) {
			if n == nil {
				return
			}
			for _, a := range n.Attributes {
				if a != nil && a.Name != "" {
					seen[at+a.Name] = true
				}
			}
			for _, c := range n.Children {
				key, _ := c.Match.(string)
				switch {
				case engine.IsBlock(c):
					walk(c, at+key+"/")
				case at != "" && key != "" && len(c.Children) > 0:
					walk(c.Children[0], at+key+"/*/")
				default:
					walk(c, at)
				}
			}
		}
		walk(g.Children[0], "")

		names := make([]string, 0, len(seen))
		for name := range seen {
			names = append(names, name)
		}
		sort.Strings(names)
		out[kind] = names
	}
	return out
})
