package pb

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
	result, err := protogen.Check(root, "pkg/kvdb/statetree/pb")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	protogen.PrintReport(&b, result)
	if len(result.Diffs) > 0 || result.Checked == 0 {
		t.Fatalf("pkg/kvdb/statetree/pb drifted from `buf generate`:\n%s", b.String())
	}
}
