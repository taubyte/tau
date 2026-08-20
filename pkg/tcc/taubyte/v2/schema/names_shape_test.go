package schema_test

import (
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// AN ATTRIBUTE'S KEY IS SEGMENTS, and its identity is those segments joined. Nothing in
// the DSL ever writes the "/" — `Key("execution", "call")` does, and the engine joins it.
//
// This used to say the opposite: that a name is flat and Path() says where it is
// authored. That produced `execution-call`, a path spelled into a name, which compiles
// onto one opaque leaf a CRDT can only replace whole.
func TestAKeyIsSegmentsNeverASlashedString(t *testing.T) {
	var walk func(nodes []*schema.Node)
	walk = func(nodes []*schema.Node) {
		for _, g := range nodes {
			if len(g.Children) == 0 {
				continue
			}
			iter := g.Children[0]
			for _, a := range iter.Attributes {
				for _, seg := range a.Segments {
					assert.Assert(t, !strings.Contains(seg, "/"),
						"%v: key segment %q of %q carries a slash", g.Match, seg, a.Name)
				}
				if len(a.Segments) == 0 {
					assert.Assert(t, !strings.Contains(a.Name, "/"),
						"%v: attribute %q carries a slash but declares no segments", g.Match, a.Name)
					continue
				}
				assert.Equal(t, a.Name, strings.Join(a.Segments, "/"),
					"%v: identity must be the key, joined", g.Match)
			}
			walk(iter.Children)
		}
	}
	walk(schema.CompileRoot().Children)
}
