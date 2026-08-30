package engine

import (
	"fmt"
	"sort"
	"strings"

	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// An authored key the schema does not have is an ERROR, reported at its own line.
//
// It used to be dropped in silence. Every misspelled field asserted something the
// compiler agreed with by saying nothing: this repo's own fixtures carried
// `source: true` on a connector whose attribute is `polls`, compiled clean, and the
// field was simply absent from the compiled object. The failure then surfaces as
// absent BEHAVIOUR — a connector that never polls, a workload with no limit — a long
// way from the line that caused it.
//
// That is the inverse of what the DSL is for. Per-field validation, completion,
// JSON-Schema emission and per-field error locations are most of the reason it exists,
// and a field name nothing ever looks for defeats all four at once.
//
// The check reads the schema rather than a list: every path an attribute can be read
// from is claimed, `Either(...)` contributes each of its branches, `All()` marks a level
// where any key is legal, and a child group claims its own name. So a kind that gains an
// attribute gains the key for free, and there is no second place to update.

// Strict turns the check on, declared on a vocabulary's ROOT. It is per-vocabulary
// rather than always-on because the two vocabularies are in different positions and
// the difference is not stylistic:
//
//   - v2 is being authored and has no deployed documents. A typo there is a bug being
//     written, and catching it costs nobody anything.
//   - v1 is FROZEN, and real projects are deployed against it. Turning this on for v1
//     rejected `replicas:` and `path:` on a database in taubyte/tb_test_project —
//     keys v1 has never read, in a repository cloned at test time, in four dreaming
//     suites at once. Every customer project with a dead key would break the same way,
//     to catch typos in a vocabulary nobody is authoring any more.
//
// That is a bad trade for a frozen oracle and a good one for a live vocabulary, so it
// is the vocabulary's statement to make.
func Strict() NodeOption {
	return func(n *Node) { n.Strict = true }
}

// claim is a prefix tree of the key paths one document may hold.
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
		_ = m
		c.any = true
	}
}

// attrClaims is what a node's attributes may be read from.
func attrClaims(n *Node, envs []string) *claim {
	c := newClaim()
	for _, attr := range n.Attributes {
		path := attr.Path
		if len(path) == 0 {
			// same fallback setAttributes uses, so the claim matches the read
			path = []StringMatch{attr.Name}
		}
		c.insert(path)
		// The environment's spelling of this field is a key the schema CAN read, so
		// it is claimed like any other — otherwise every override is an unknown key.
		// Claimed only where the attribute is marked, so `id_preview` stays a typo.
		if attr.Meta["perEnv"] == true {
			for _, env := range envs {
				if ep := envPath(path, env); ep != nil {
					c.insert(ep)
				}
			}
		}
		if len(attr.Compat) > 0 {
			c.insert(attr.Compat)
		}
	}
	return c
}

// checkKeys rejects any key in this node's document that the schema cannot read.
func checkKeys(n *Node, query *yaseer.Query, envs []string) error {
	// A node with no attributes and no children expresses no opinion about its
	// document — it reads nothing and produces an empty object either way. "Every
	// key is unknown" is vacuous there rather than stricter, so it is left alone.
	if len(n.Attributes) == 0 && len(n.Children) == 0 {
		return nil
	}

	if !n.Group {
		c := attrClaims(n, envs)
		if n.Nested {
			// a nested resource's child groups are keys of its own document
			for _, child := range n.Children {
				c.claimGroup(child)
			}
		}
		return c.walk(query)
	}

	// A container holds its own fields under the config leaf and its child groups
	// beside it — and a child group may ALSO be authored inside config, which
	// loadChildren accepts, so both levels admit both.
	leaf := LeafOf(n)
	outer := newClaim()
	outer.child(leaf).leaf = true // its contents are the inner pass
	for _, child := range n.Children {
		outer.claimGroup(child)
	}
	if err := outer.walk(query); err != nil {
		return err
	}

	inner := attrClaims(n, envs)
	for _, child := range n.Children {
		inner.claimGroup(child)
	}
	return inner.walk(query.Get(leaf))
}

func (c *claim) walk(query *yaseer.Query) error {
	if c.any && len(c.kids) == 0 {
		return nil
	}

	// STRICT INSIDE A DOCUMENT, PERMISSIVE ON THE FILESYSTEM.
	//
	// Query.List() enumerates a mapping's keys, and falls back to a DIRECTORY's
	// entries when the node is not a mapping. Those are not the same claim: a config
	// tree lives inside a real repository, so the project root legitimately contains
	// `.git`, `docs/`, a README — none of which the schema names and none of which is
	// a mistake. Rejecting them refuses every project on disk, which is how the
	// dreaming suite found this.
	//
	// A key typed into a yaml document has no such excuse. So the check applies only
	// where the node genuinely IS a mapping.
	//
	// The cost, recorded: a mistyped DIRECTORY name — `fuctions/` — is still silent.
	// That one belongs to the publish-time pass, which can see that a project declares
	// no functions at all.
	var doc any
	if err := query.Value(&doc); err != nil {
		return nil
	}
	if !isMapping(doc) {
		return nil
	}

	keys, err := query.List()
	if err != nil {
		// nothing to enumerate. An absent required field is already the business
		// of setAttributes.
		return nil
	}
	for _, key := range keys {
		sub, known := c.kids[key]
		if !known {
			if c.any {
				continue
			}
			// Resolve the node before asking where it is. Query.location() walks up
			// to the nearest RESOLVED ancestor, so an unread Get inherits the
			// document's own position and every top-level mistake reports 1:1.
			//
			// Resolving sets the position of the VALUE, which is the same convention
			// every other error here follows ("3:7: invalid variable name" points at
			// the value too). For a scalar that is the offending line exactly; for a
			// key whose value is a block mapping it is the mapping's first child, one
			// line below. yaml.v3 exposes no position for the key itself, so that is
			// the closest available and it stays inside the block it names.
			at := query.Get(key)
			var ignored any
			at.Value(&ignored)
			filePath, line, column := at.Location()
			return located(filePath, line, column,
				fmt.Errorf("unknown key '%s'%s", key, c.suggest()))
		}
		if sub.leaf && len(sub.kids) == 0 {
			// The key is real, and the walk would stop here — which left the OTHER
			// half of the same silence: a BLOCK written where a value belongs.
			//
			//	transformers:      the attribute is a list; a mapping here reads as
			//	    redact:        nothing, so the transformers simply never run and
			//	        order: 1   no key in the document is unknown
			//
			// A wrong-typed scalar is already an error (setAttributes), and no
			// attribute type is a mapping, so this needs no exception.
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
