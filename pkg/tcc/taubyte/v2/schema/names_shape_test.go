package schema_test

import (
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// An attribute's NAME is a flat, unique key; where it is AUTHORED is Path(). Slashes in
// the name were a second convention beside the DSL's, and they made a generated Go/TS
// identifier impossible.
func TestNoSlashInAttributeNames(t *testing.T) {
	var walk func(nodes []*schema.Node)
	walk = func(nodes []*schema.Node) {
		for _, g := range nodes {
			if len(g.Children) == 0 {
				continue
			}
			iter := g.Children[0]
			for _, a := range iter.Attributes {
				assert.Assert(t, !strings.Contains(a.Name, "/"),
					"%v: attribute %q carries a slash; the name is flat and Path() says where it is authored", g.Match, a.Name)
			}
			walk(iter.Children)
		}
	}
	walk(schema.CompileRoot().Children)
}
