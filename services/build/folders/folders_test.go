package folders

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/build/backend"
)

type fallthroughSeam struct{ asked *string }

func (f fallthroughSeam) Checkout(_ context.Context, key, _, _ string) (string, func(), error) {
	*f.asked = key
	return "inner", func() {}, nil
}

func (f fallthroughSeam) Confirm(_ context.Context, key, _, _ string) error {
	*f.asked = key
	return nil
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return strings.TrimSpace(string(out))
}

func TestALocalFolderIsCheckedOutAtTheCommitAsked(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644))
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "one")
	first := gitIn(t, dir, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two"), 0o644))
	gitIn(t, dir, "commit", "-qam", "two")
	second := gitIn(t, dir, "rev-parse", "HEAD")

	const key = "github.com/acme/app"
	f, err := New(map[string]string{key: dir})
	require.NoError(t, err)
	var asked string
	checkout, confirm := f.Checkout(fallthroughSeam{&asked}), f.Confirm(fallthroughSeam{&asked})

	at, done, err := checkout.Checkout(t.Context(), key, "main", first)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(at, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "one", string(got))
	done()

	require.NoError(t, confirm.Confirm(t.Context(), key, "refs/heads/main", second))
	require.ErrorIs(t, confirm.Confirm(t.Context(), key, "refs/heads/main", first), backend.ErrNotAtCommit)
	require.Empty(t, asked)

	dirOut, _, err := checkout.Checkout(t.Context(), "github.com/acme/other", "main", first)
	require.NoError(t, err)
	require.Equal(t, "inner", dirOut)
	require.Equal(t, "github.com/acme/other", asked)
	require.NoError(t, confirm.Confirm(t.Context(), "github.com/acme/elsewhere", "refs/heads/main", first))
	require.Equal(t, "github.com/acme/elsewhere", asked)

	require.ErrorContains(t, f.clone.Confirm(t.Context(), "github.com/acme/unknown", "refs/heads/main", first), "is not a local folder")
}
