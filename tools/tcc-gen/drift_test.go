package main

import (
	"os"
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	"github.com/taubyte/tau/tools/tcc-gen/internal/gen"
)

// TestNoDrift fails if the committed generated files are out of sync with what
// tcc-gen produces from the current DSL — the go-test equivalent of
// `tcc-gen --check`. It byte-compares the structureSpec structs and the TS
// schema, and name-set compares the pkg/schema accessors (custom.go supplements
// live in their own files, so they don't register as drift). Regenerate and
// adopt (`go run ./tools/tcc-gen --out <dir>` then copy in) to fix.
func TestNoDrift(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findRepoRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := gen.Generate(schema.GenerationRoot())
	if err != nil {
		t.Fatal(err)
	}
	diffs, err := gen.Check(root, generated)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) > 0 {
		var b strings.Builder
		gen.PrintReport(&b, generated, diffs)
		t.Fatalf("generated files drift from the committed tree — regenerate with tcc-gen and adopt:\n%s", b.String())
	}
}
