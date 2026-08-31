package proto

import (
	"os"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/protogen"
)

// TestNoDrift fails if the committed pkg/v2/proto/gen tree is out of sync
// with what the pinned buf toolchain (see buf.gen.yaml) produces from the
// committed .proto sources — the buf equivalent of tools/tcc-gen's own
// TestNoDrift, part of the ordinary `go test ./...` sweep so CI runs it too.
// Regenerate and adopt with `make proto-v2`.
func TestNoDrift(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := protogen.FindRepoRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}

	result, err := protogen.Check(root, "pkg/v2/proto")
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	protogen.PrintReport(&b, result)
	t.Log(b.String())

	if len(result.Diffs) > 0 {
		t.Fatalf("pkg/v2/proto/gen drifted from `buf generate` — regenerate with `make proto-v2` and commit the result:\n%s", b.String())
	}
}
