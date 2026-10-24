package volume

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAPATHIsFollowedToWhereItIsOnDisk, which is what closeDirs closes: every
// link along the name followed, a ".." taken from where the link led, and a
// name that leads out or round in a loop refused rather than guessed at.
func TestAPathIsFollowedToWhereItIsOnDisk(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0o750))
	for link, target := range map[string]string{
		"l":     "a/b",
		"a/up":  "../a/b/c",
		"out":   "../..",
		"loop1": "loop2",
		"loop2": "loop1",
	} {
		require.NoError(t, os.Symlink(target, filepath.Join(dir, filepath.FromSlash(link))))
	}
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer root.Close()

	for name, want := range map[string]string{
		"a/b/c":    "a/b/c",
		"l/c":      "a/b/c",
		"a/up":     "a/b/c",
		"l/c/../c": "a/b/c",
		"l/../..":  ".",
	} {
		at, err := reached(root, filepath.FromSlash(name))
		require.NoError(t, err, name)
		require.Equal(t, filepath.FromSlash(want), at, name)
	}

	for _, name := range []string{"out/x", "loop1", "missing/x"} {
		_, err := reached(root, name)
		require.Error(t, err, name)
	}
}
