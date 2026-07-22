package session

import (
	"sort"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

func TestCoW(t *testing.T) {
	base := afero.NewMemMapFs()
	assert.NilError(t, afero.WriteFile(base, "/dir/a.yaml", []byte("A"), 0o644))
	assert.NilError(t, afero.WriteFile(base, "/dir/b.yaml", []byte("B"), 0o644))

	c := NewCoW(base)

	// reads fall through to base
	got, err := afero.ReadFile(c, "/dir/a.yaml")
	assert.NilError(t, err)
	assert.Equal(t, string(got), "A")

	// write a new file -> overlay, base untouched
	assert.NilError(t, afero.WriteFile(c, "/dir/c.yaml", []byte("C"), 0o644))
	_, err = base.Stat("/dir/c.yaml")
	assert.Assert(t, err != nil, "base must not see overlay writes")

	// modify a base file -> copy-up, base untouched
	assert.NilError(t, afero.WriteFile(c, "/dir/a.yaml", []byte("A2"), 0o644))
	got, _ = afero.ReadFile(c, "/dir/a.yaml")
	assert.Equal(t, string(got), "A2")
	baseA, _ := afero.ReadFile(base, "/dir/a.yaml")
	assert.Equal(t, string(baseA), "A", "base file must be unchanged")

	// delete a base-only file -> gone from Stat/Open/Readdir; base untouched
	assert.NilError(t, c.Remove("/dir/b.yaml"))
	_, err = c.Stat("/dir/b.yaml")
	assert.Assert(t, err != nil, "deleted file must not Stat")
	names := listDir(t, c, "/dir")
	assert.DeepEqual(t, names, []string{"a.yaml", "c.yaml"}) // no b.yaml
	_, err = base.Stat("/dir/b.yaml")
	assert.NilError(t, err, "base file must survive the CoW delete")

	// re-create the deleted file -> visible again
	assert.NilError(t, afero.WriteFile(c, "/dir/b.yaml", []byte("B2"), 0o644))
	got, _ = afero.ReadFile(c, "/dir/b.yaml")
	assert.Equal(t, string(got), "B2")

	// changeset: written overlay files + (no longer) deleted
	written, deleted := c.Changed()
	sort.Strings(written)
	assert.DeepEqual(t, written, []string{"/dir/a.yaml", "/dir/b.yaml", "/dir/c.yaml"})
	assert.Equal(t, len(deleted), 0, "b.yaml was re-created, so not deleted anymore")
}

func listDir(t *testing.T, fs afero.Fs, dir string) []string {
	t.Helper()
	f, err := fs.Open(dir)
	assert.NilError(t, err)
	defer f.Close()
	names, err := f.Readdirnames(-1)
	assert.NilError(t, err)
	sort.Strings(names)
	return names
}
