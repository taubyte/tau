package jobs

import (
	"os"
	"path/filepath"
	"testing"
)

// A committed link as a function folder would make the build bind-mount
// whatever it points to, read-write.
func TestOpSourcePathRefusesLinkOutOfClone(t *testing.T) {
	clone, outside := t.TempDir(), t.TempDir()
	functions := filepath.Join(clone, "functions")
	if err := os.MkdirAll(filepath.Join(functions, "good"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(functions, "evil")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("good", filepath.Join(functions, "alias")); err != nil {
		t.Fatal(err)
	}

	c := Context{gitDir: clone}
	op := func(name string) Op { return Op{name: name, pathVariable: "functions"} }

	if _, err := c.opSourcePath(op("evil")); err == nil {
		t.Fatal("a function folder linking out of the clone was accepted")
	}
	for _, name := range []string{"good", "alias"} {
		if _, err := c.opSourcePath(op(name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
