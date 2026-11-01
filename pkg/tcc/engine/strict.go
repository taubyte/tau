package engine

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// An authored key the schema does not have is an error at its own line: dropped
// in silence, a misspelled field surfaces as absent behaviour far from its
// cause. The claims are read from the schema: every attribute path, each
// Either() branch, All() for any key, and each child group's name.

type claim struct {
	kids map[string]*claim
	// any: an All() matcher reaches this level, so any key here is legal. Also set
	// where entries are keyed by a resource NAME the author chooses.
	any bool
	// leaf: an attribute's value is read here. With no kids, nothing below is schema
	// and the walk stops; with kids, a branch of an Either() both holds a value (via
	// Captures()) and has fields under it, so the walk continues.
	leaf bool
}

func newClaim() *claim { return &claim{kids: map[string]*claim{}} }

func (c *claim) child(k string) *claim {
	if c.kids[k] == nil {
		c.kids[k] = newClaim()
	}
	return c.kids[k]
}

func (c *claim) insert(path []StringMatch) {
	if len(path) == 0 {
		c.leaf = true
		return
	}
	switch m := path[0].(type) {
	case string:
		c.child(m).insert(path[1:])
	case FiniteMatcher:
		// Either("http","function") — every branch is a legal key
		for _, v := range m.Values() {
			c.child(v).insert(path[1:])
		}
	case StringMatcher:
		// All() — this level is unconstrained, and so is everything below it
		c.any = true
		c.leaf = true
	default:
		// an unknown matcher constrains nothing rather than rejecting everything
		c.any = true
		c.leaf = true
	}
}

// insertBranch claims a Captures() path. Its last segment is the KEY an author picks,
// and what sits under that key is a block — empty when the branch takes no fields — so
// it is claimed as a level, never as a value.
func (c *claim) insertBranch(path []StringMatch) {
	if len(path) == 0 {
		return
	}
	if len(path) > 1 {
		if m, ok := path[0].(string); ok {
			c.child(m).insertBranch(path[1:])
			return
		}
		if m, ok := path[0].(FiniteMatcher); ok {
			for _, v := range m.Values() {
				c.child(v).insertBranch(path[1:])
			}
			return
		}
		c.insert(path)
		return
	}
	switch m := path[0].(type) {
	case string:
		c.child(m)
	case FiniteMatcher:
		for _, v := range m.Values() {
			c.child(v)
		}
	default:
		c.insert(path)
	}
}

// claimGroup records a child group's own key. What is INSIDE it is checked when that
// node is loaded, so this stops here.
func (c *claim) claimGroup(child *Node) {
	switch m := child.Match.(type) {
	case string:
		g := c.child(m)
		g.any = true
	case FiniteMatcher:
		for _, v := range m.Values() {
			c.child(v).any = true
		}
	default:
		c.any = true
	}
}

// attrClaims is what a node's attributes may be read from.
func attrClaims(n *Node, vars Vars) *claim {
	c := newClaim()
	for _, attr := range n.Attributes {
		path := attr.Path
		if len(path) == 0 {
			// same fallback setAttributes uses, so the claim matches the read
			path = []StringMatch{attr.Name}
		}
		if attr.Captures {
			c.insertBranch(path)
		} else {
			c.insert(path)
		}
		// Every variant spelling of this field is claimed, or every override is an
		// unknown key: the union of each variable's values, not the cross product, and
		// only where the attribute is marked.
		for _, name := range VariesBy(attr) {
			for _, value := range vars.Values(name) {
				if vp := variantPath(path, value); vp != nil {
					c.insert(vp)
				}
			}
		}
	}
	return c
}

// checkKeys rejects any key in this node's document that the schema cannot read.
func checkKeys(n *Node, query *yaseer.Query, vars Vars) error {
	// A node with no attributes and no children expresses no opinion about its
	// document — it reads nothing and produces an empty object either way. "Every
	// key is unknown" is vacuous there rather than stricter, so it is left alone.
	if len(n.Attributes) == 0 && len(n.Children) == 0 {
		return nil
	}

	if !n.Group {
		c := attrClaims(n, vars)
		if n.Nested {
			// a nested resource's child groups are keys of its own document
			for _, child := range n.Children {
				c.claimGroup(child)
			}
		}
		return c.walk(query)
	}

	// A container holds its own fields under the leaf and its child groups beside
	// it, and a child group may also be authored inside the leaf, so both levels
	// admit both.
	leaf := LeafOf(n)
	outer := newClaim()
	outer.child(leaf).leaf = true // its contents are the inner pass
	for _, child := range n.Children {
		outer.claimGroup(child)
	}
	if err := outer.walk(query); err != nil {
		return err
	}

	inner := attrClaims(n, vars)
	for _, child := range n.Children {
		inner.claimGroup(child)
	}
	return inner.walk(query.Get(leaf))
}

func (c *claim) walk(query *yaseer.Query) error {
	if c.any && len(c.kids) == 0 {
		return nil
	}

	// Strict inside a document, permissive on the filesystem: a project root holds
	// `.git`, `docs/` and a README the schema never names. A mistyped directory name
	// is still silent; that belongs to the publish-time pass.
	var doc any
	if err := query.Value(&doc); err != nil {
		return nil
	}
	if !isMapping(doc) {
		return nil
	}

	keys, err := query.List()
	if err != nil {
		// Nothing to enumerate; an absent required field is setAttributes' business.
		return nil
	}
	keys = slices.Sorted(slices.Values(keys))
	for _, key := range keys {
		sub, known := c.kids[key]
		if !known {
			if c.any {
				continue
			}
			// Resolve the node first, so the error points at its value, the convention
			// every other error here follows; yaml.v3 gives no position for the key.
			at := query.Get(key)
			var ignored any
			at.Value(&ignored)
			filePath, line, column := at.Location()
			return located(filePath, line, column,
				fmt.Errorf("unknown key '%s'%s", key, c.suggest()))
		}
		if sub.leaf && len(sub.kids) == 0 {
			// A block written where a value belongs (a mapping under a list attribute) would
			// read as nothing with no key unknown. No attribute type is a mapping.
			if !sub.any && isBlockAt(query, key) {
				at := query.Get(key)
				filePath, line, column := at.Location()
				return located(filePath, line, column,
					fmt.Errorf("attribute '%s' takes a value, not a block", key))
			}
			continue
		}
		if err := sub.walk(query.Get(key)); err != nil {
			return err
		}
	}
	return nil
}

// suggest names what WOULD have been accepted here. A rejection that does not say
// what is legal turns a typo into a search through the schema.
func (c *claim) suggest() string {
	if len(c.kids) == 0 {
		return ""
	}
	keys := make([]string, 0, len(c.kids))
	for k := range c.kids {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	const max = 12
	trimmed := ""
	if len(keys) > max {
		trimmed = fmt.Sprintf(" and %d more", len(keys)-max)
		keys = keys[:max]
	}
	return fmt.Sprintf("; valid keys here: %s%s", strings.Join(keys, ", "), trimmed)
}

// isBlockAt reports whether the document holds a mapping under key.
func isBlockAt(query *yaseer.Query, key string) bool {
	var v any
	if query.Get(key).Value(&v) != nil {
		return false
	}
	return isMapping(v)
}

// isMapping reports whether a resolved node is a yaml mapping, which is the only
// place an unknown key is the author's mistake rather than a neighbouring file.
func isMapping(v any) bool {
	switch v.(type) {
	case map[string]any, map[any]any:
		return true
	}
	return false
}
