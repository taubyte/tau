package proto

import (
	"os"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/protogen"
)

func TestNoDrift(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := protogen.FindRepoRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}

	result, err := protogen.Check(root, "pkg/proto")
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	protogen.PrintReport(&b, result)
	t.Log(b.String())

	if len(result.Diffs) > 0 {
		t.Fatalf("pkg/proto/gen drifted from `buf generate` — regenerate with `make proto-v2` and commit the result:\n%s", b.String())
	}
}
