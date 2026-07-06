package gen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

func TestWriteTo(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join("pkg", "schema", "foo", "set.go")
	files := map[string][]byte{rel: []byte("package foo\n")}

	if err := WriteTo(dir, files); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("expected file written: %v", err)
	}
	if string(got) != "package foo\n" {
		t.Errorf("content = %q", got)
	}
}

// TestCheck exercises the drift report end to end against a synthetic repo root:
// a matching-but-drifted file, a generated file with no counterpart, and a
// non-schema file that must be skipped.
func TestCheck(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "pkg", "schema", "foo")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// real foo/set.go declares A and B
	if err := os.WriteFile(filepath.Join(realDir, "set.go"), []byte("package foo\nfunc A() {}\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gen := map[string][]byte{
		// same file: generated has A + C — extra C, missing B
		filepath.Join("pkg", "schema", "foo", "set.go"): []byte("package foo\nfunc A() {}\nfunc C() {}\n"),
		// no real counterpart on disk: everything is "extra in generated"
		filepath.Join("pkg", "schema", "bar", "set.go"): []byte("package bar\nfunc D() {}\n"),
		// non-schema path: must be skipped entirely
		filepath.Join("pkg", "specs", "structure", "foo.go"): []byte("package structureSpec\nfunc Ignored() {}\n"),
	}

	diffs, err := Check(root, gen)
	if err != nil {
		t.Fatal(err)
	}

	byRel := map[string]Diff{}
	for _, d := range diffs {
		byRel[d.Rel] = d
	}

	foo := byRel[filepath.Join("pkg", "schema", "foo", "set.go")]
	if got := strings.Join(foo.ExtraInGen, ","); got != "C" {
		t.Errorf("foo extra-in-gen = %q, want C", got)
	}
	if got := strings.Join(foo.MissingInGen, ","); got != "B" {
		t.Errorf("foo missing-in-gen = %q, want B", got)
	}

	bar := byRel[filepath.Join("pkg", "schema", "bar", "set.go")]
	if got := strings.Join(bar.ExtraInGen, ","); got != "D" {
		t.Errorf("bar (no real file) extra-in-gen = %q, want D", got)
	}

	if _, ok := byRel[filepath.Join("pkg", "specs", "structure", "foo.go")]; ok {
		t.Error("non-schema file must be skipped by Check")
	}
}

func TestPrintReport(t *testing.T) {
	gen := map[string][]byte{
		filepath.Join("pkg", "schema", "a", "set.go"):      nil,
		filepath.Join("pkg", "schema", "b", "set.go"):      nil,
		filepath.Join("pkg", "specs", "structure", "a.go"): nil, // must not be reported
	}
	diffs := []Diff{{
		Rel:          filepath.Join("pkg", "schema", "b", "set.go"),
		ExtraInGen:   []string{"X"},
		MissingInGen: []string{"Y"},
	}}

	var buf bytes.Buffer
	PrintReport(&buf, gen, diffs)
	out := buf.String()

	for _, want := range []string{
		"OK    " + filepath.Join("pkg", "schema", "a", "set.go"),
		"DIFF  " + filepath.Join("pkg", "schema", "b", "set.go"),
		"only in generated: [X]",
		"only in hand-written: [Y]",
		"2 schema files checked, 1 with differences",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "structure") {
		t.Error("PrintReport must not list non-schema files")
	}
}

// TestStructFieldNameSanitize covers the hyphen-fallback branch: an attribute with
// a matcher path and no Field override derives its name from the (hyphenated)
// attribute name, which must be sanitized into a valid Go identifier.
func TestStructFieldNameSanitize(t *testing.T) {
	a := engine.String("git-provider", engine.Path("source", engine.Either("github")), engine.Key())
	if got := structFieldName("websites", a); got != "GitProvider" {
		t.Errorf("structFieldName = %q, want GitProvider", got)
	}
}
