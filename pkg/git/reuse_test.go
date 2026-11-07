package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// GitHub serves any reachable commit by hash; a local origin must be told to.
	gitCLI(t, origin, "config", "uploadpack.allowReachableSHA1InWant", "true")
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

func headOf(t *testing.T, origin string) string {
	return strings.TrimSpace(gitCLI(t, origin, "rev-parse", "HEAD"))
}

func openAt(t *testing.T, origin, dir, branch, commit string) *Repository {
	r, err := New(context.Background(), URL("file://"+origin), Root(dir), Branch(branch), Shallow(), Reuse(commit), Output(os.Stderr))
	assert.NilError(t, err)
	return r
}

func readFile(t *testing.T, dir, file string) string {
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return ""
	}
	return string(b)
}

// A job for commit A that runs after B was pushed builds A, not the tip.
func TestReuseEndsOnCommitBehindTip(t *testing.T) {
	origin, dir := originWith(t, "a.txt", "1"), filepath.Join(t.TempDir(), "w")
	assert.NilError(t, os.MkdirAll(dir, 0755))
	pushFile(t, origin, "a.txt", "2")
	a := headOf(t, origin)
	pushFile(t, origin, "a.txt", "3")

	openAt(t, origin, dir, "main", a) // fresh clone
	assert.Equal(t, readFile(t, dir, "a.txt"), "2")
	openAt(t, origin, dir, "main", a) // reused clone
	assert.Equal(t, readFile(t, dir, "a.txt"), "2")
	openAt(t, origin, dir, "main", "")
	assert.Equal(t, readFile(t, dir, "a.txt"), "3")
}

// One repo dir serves every branch: a job on another branch gets that branch.
func TestReuseSwitchesBranch(t *testing.T) {
	origin, dir := originWith(t, "a.txt", "main"), filepath.Join(t.TempDir(), "w")
	assert.NilError(t, os.MkdirAll(dir, 0755))
	gitCLI(t, origin, "branch", "dev")
	gitCLI(t, origin, "checkout", "-q", "dev")
	pushFile(t, origin, "a.txt", "dev1")
	d1 := headOf(t, origin)
	pushFile(t, origin, "a.txt", "dev2")
	gitCLI(t, origin, "checkout", "-q", "main")

	openAt(t, origin, dir, "main", "")
	assert.Equal(t, readFile(t, dir, "a.txt"), "main")
	openAt(t, origin, dir, "dev", d1)
	assert.Equal(t, readFile(t, dir, "a.txt"), "dev1")
	openAt(t, origin, dir, "main", "")
	assert.Equal(t, readFile(t, dir, "a.txt"), "main")
}
