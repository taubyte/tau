package schema_test

import (
	"context"
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// Branch shapes that turn up in real repositories: the default, a topic branch, a
// release line, a bot's, and one holding the escape character.
var branchShapes = []struct {
	branch, segment string
}{
	{"main", "main"},
	{"feature/x", "feature%2Fx"},
	{"release/1.2", "release%2F1.2"},
	{"renovate/some-dep-1.2.3", "renovate%2Fsome-dep-1.2.3"},

	// `%` is a legal ref character (`git check-ref-format refs/heads/fix/100%-coverage`
	// exits 0), so it is escaped too — otherwise the encoding is not reversible and the
	// branch below would share a segment with a branch literally named `fix%2F100%-…`.
	{"fix/100%-coverage", "fix%2F100%25-coverage"},

	// the pair that says why: two different branches, two different segments
	{"a/b", "a%2Fb"},
	{"a%2Fb", "a%252Fb"},

	// nothing about the encoding is ASCII-only: only `/` and `%` are touched, so a
	// multi-byte rune stays legible rather than becoming %C3%A9
	{"feature/café", "feature%2Fcafé"},
}

func TestBranchIsOneSegmentAndComesBack(t *testing.T) {
	seen := map[string]string{}
	for _, shape := range branchShapes {
		got := schema.EncodeBranch(shape.branch)
		assert.Equal(t, got, shape.segment, "encoding %q", shape.branch)
		assert.Assert(t, !strings.Contains(got, "/"), "%q encoded to %q, which is still two segments", shape.branch, got)

		back, err := schema.DecodeBranch(got)
		assert.NilError(t, err)
		assert.Equal(t, back, shape.branch, "round trip of %q through %q", shape.branch, got)

		if other, collides := seen[got]; collides {
			t.Fatalf("%q and %q both encode to %q", other, shape.branch, got)
		}
		seen[got] = shape.branch
	}
}

func TestBranchDecodeRefusesWhatItNeverWrote(t *testing.T) {
	for _, segment := range []string{
		"%6Dain",     // a valid escape this encoding never emits
		"release%2f", // lowercase hex is a second spelling of the same segment
		"release%2",  // truncated
		"release%",   // truncated to nothing
		"100%-coverage",
	} {
		_, err := schema.DecodeBranch(segment)
		assert.ErrorContains(t, err, "decoding branch", "%q was accepted", segment)
	}
}

func TestCompiledIndexKeepsTheBranchInOneSegment(t *testing.T) {
	for _, shape := range branchShapes {
		t.Run(shape.branch, func(t *testing.T) {
			c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch(shape.branch))
			assert.NilError(t, err)
			obj, _, err := c.Compile(context.Background())
			assert.NilError(t, err)

			flat := obj.Flat()
			idx, ok := flat["indexes"].(map[string]any)
			assert.Assert(t, ok, "the compiled object carries no index")
			project, _ := flat["object"].(map[string]any)["id"].(string)
			assert.Assert(t, project != "")

			// a resource at the project root: branches/<b>/projects/<pid>/cache/<rid>
			values, ok := idx["projects/"+project+"/cache/orders/links"].([]string)
			assert.Assert(t, ok, "no index entry for the root cache")
			assert.Equal(t, len(values), 1)

			segments := strings.Split(values[0], "/")
			assert.Equal(t, len(segments), 6, "the value is %q, which is not one segment per level", values[0])
			assert.Equal(t, segments[0], "branches")
			assert.Equal(t, segments[2], "projects")
			assert.Equal(t, segments[3], project)
			assert.Equal(t, segments[4], "cache")

			back, err := schema.DecodeBranch(segments[1])
			assert.NilError(t, err)
			assert.Equal(t, back, shape.branch, "the value is %q", values[0])
		})
	}
}
