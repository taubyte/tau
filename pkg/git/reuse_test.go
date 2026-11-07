package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func gitCLI(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	return string(out)
}

func originWith(t *testing.T, file, body string) string {
	origin := t.TempDir()
	gitCLI(t, origin, "init", "-q", "-b", "main")
	pushFile(t, origin, file, body)
	return origin
}

func pushFile(t *testing.T, origin, file, body string) {
	assert.NilError(t, os.WriteFile(filepath.Join(origin, file), []byte(body), 0644))
	gitCLI(t, origin, "add", ".")
	gitCLI(t, origin, "commit", "-q", "-m", file)
}

func open(t *testing.T, origin, dir string) *Repository {
	r, err := New(context.Background(), URL("file://"+origin), Root(dir), Branch("main"), Shallow(), Reuse(""), Output(os.Stderr))
	assert.NilError(t, err)
	return r
}

func TestReuseFetchesNotClones(t *testing.T) {
	origin, dir := originWith(t, "a.txt", "1"), filepath.Join(t.TempDir(), "w")
	assert.NilError(t, os.MkdirAll(dir, 0755))
	assert.Assert(t, open(t, origin, dir).Cloned())

	pushFile(t, origin, "b.txt", "2")
	assert.Assert(t, !open(t, origin, dir).Cloned())
	_, err := os.Stat(filepath.Join(dir, "b.txt"))
	assert.NilError(t, err)
}

func TestReuseDropsStaleFiles(t *testing.T) {
	origin, dir := originWith(t, "a.txt", "1"), filepath.Join(t.TempDir(), "w")
	assert.NilError(t, os.MkdirAll(dir, 0755))
	open(t, origin, dir)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "stale.o"), []byte("x"), 0644))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("edited"), 0644))

	assert.Assert(t, !open(t, origin, dir).Cloned())
	_, err := os.Stat(filepath.Join(dir, "stale.o"))
	assert.Assert(t, os.IsNotExist(err))
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	assert.Equal(t, string(b), "1")
}

func TestReuseCorruptDirFallsBackToClone(t *testing.T) {
	origin, dir := originWith(t, "a.txt", "1"), filepath.Join(t.TempDir(), "w")
	assert.NilError(t, os.MkdirAll(dir, 0755))
	open(t, origin, dir)
	assert.NilError(t, os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[[garbage"), 0644))

	assert.Assert(t, open(t, origin, dir).Cloned())
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	assert.Equal(t, string(b), "1")
}
