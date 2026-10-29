package pb

import (
	"os"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/protogen"
)

// TestNoDrift fails if the generated files here differ from what the pinned
// plugins in buf.gen.yaml produce from the .proto sources. Regenerate with
// `go generate` in this directory.
func TestNoDrift(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := protogen.FindRepoRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	result, err := protogen.Check(root, "pkg/kvdb/mst/pb")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	protogen.PrintReport(&b, result)
	if len(result.Diffs) > 0 || result.Checked == 0 {
		t.Fatalf("pkg/kvdb/mst/pb drifted from `buf generate`:\n%s", b.String())
	}
}
