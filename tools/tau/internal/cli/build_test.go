package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/vault"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/tools/tau/internal/project"
)

// shell is an Engine that runs a recipe's commands on this machine, in the
// resource's directory of the checkout it was handed — what a container does
// with a bind mount, without a daemon.
type shell struct {
	mu   sync.Mutex
	seen []artifact.Spec
	// saw is what each build found in its checkout, relative to it.
	saw [][]string
}

func (s *shell) Make(ctx context.Context, spec artifact.Spec, log io.Writer) (io.ReadCloser, error) {
	var files []string
	filepath.WalkDir(spec.Dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(spec.Dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	s.mu.Lock()
	s.seen = append(s.seen, spec)
	s.saw = append(s.saw, files)
	s.mu.Unlock()

	for _, step := range spec.Steps {
		c := exec.CommandContext(ctx, "/bin/sh", "-c", strings.Join(step.Commands, " && "))
		c.Dir = filepath.Join(spec.Dir, spec.Path)
		c.Env = []string{"PATH=" + os.Getenv("PATH")}
		for k, v := range spec.Variables {
			c.Env = append(c.Env, k+"="+v)
		}
		c.Stdout, c.Stderr = log, log
		if err := c.Run(); err != nil {
			return nil, err
		}
	}
	return os.Open(filepath.Join(spec.Dir, spec.Path, spec.Output))
}

const helloBuild = `output: out/hello.wasm
build:
    steps:
        - name: build
          image: busybox:1.36
          commands:
              - mkdir -p out
              - cat ../shared/greeting > out/hello.wasm
`

// buildable is a starter project whose package builds with the shell engine,
// reading a sibling directory the way a monorepo dependency is read.
func buildable(t *testing.T, x *tester) {
	t.Helper()
	x.ok("init", "shop", "--dir", x.dir)
	pkg := filepath.Join(x.dir, "package", "hello_code.yaml")
	body, err := os.ReadFile(pkg)
	require.NoError(t, err)
	// The starter's own output and build give way to this one; its id stays, wherever
	// init minted it.
	var kept []string
	inBuild := false
	for _, line := range strings.Split(string(body), "\n") {
		top := line != "" && line[0] != ' '
		if top {
			inBuild = strings.HasPrefix(line, "build:")
		}
		if inBuild || strings.HasPrefix(line, "output:") {
			continue
		}
		kept = append(kept, line)
	}
	rewritten := strings.TrimRight(strings.Join(kept, "\n"), "\n") + "\n" + helloBuild
	require.NoError(t, os.WriteFile(pkg, []byte(rewritten), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(x.dir, "services", "hello"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "services", "hello", "build.toml"), []byte("[package]"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(x.dir, "services", "shared"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "services", "shared", "greeting"), []byte("hello from the tree"), 0o644))
	x.engine = &shell{}
}

func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := c.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func readBuilt(t *testing.T, dir, environment string) []built {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, OutDir, environment, "recipes.json"))
	require.NoError(t, err)
	var out []built
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func TestABuildOnThisMachineIsTheCloudsBuild(t *testing.T) {
	x := newTester(t)
	buildable(t, x)

	var out buildOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", x.dir, "-o", "json")), &out))
	require.Len(t, out.Built, 1)
	b := out.Built[0]
	require.Equal(t, "prod", b.Environment)
	require.Equal(t, "package/hello_code", b.Kind+"/"+b.Name)
	require.Equal(t, filepath.Join(OutDir, "prod", "package", "hello_code"), b.File)

	product, err := os.ReadFile(filepath.Join(x.dir, b.File))
	require.NoError(t, err)
	require.Equal(t, "hello from the tree", string(product))
	require.Equal(t, []built{b}, readBuilt(t, x.dir, "prod"))

	e := x.engine.(*shell)
	require.Len(t, e.seen, 1)
	require.Equal(t, b.Recipe, e.seen[0].Digest(), "the recipe is named by the digest a job log prints")
	require.Equal(t, filepath.Join("services", "hello"), e.seen[0].Path)
	require.NotEqual(t, x.dir, e.seen[0].Dir, "it built in a copy")
	require.NoDirExists(t, filepath.Join(x.dir, "services", "hello", "out"), "and the developer's tree was not written into")
	require.NotContains(t, e.saw[0], ".tau/out/prod/recipes.json", "nothing under .tau is part of the tree")

	text := x.ok("build", x.dir)
	require.Contains(t, text, "package/hello_code")
	require.Contains(t, text, b.Recipe[:12])
}

func TestWhatTreeABuildReads(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, ".gitignore"), []byte("secret.txt\n"), 0o644))
	gitAt(t, x.dir, "init", "-q", "-b", "main")
	gitAt(t, x.dir, "add", "-A")
	gitAt(t, x.dir, "commit", "-q", "-m", "one")
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "secret.txt"), []byte("ignored"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "services", "shared", "greeting"), []byte("uncommitted"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "services", "untracked"), []byte("new"), 0o644))

	x.ok("build", x.dir)
	product, err := os.ReadFile(filepath.Join(x.dir, OutDir, "prod", "package", "hello_code"))
	require.NoError(t, err)
	require.Equal(t, "uncommitted", string(product))
	e := x.engine.(*shell)
	require.Contains(t, e.saw[0], "services/untracked")
	require.NotContains(t, e.saw[0], "secret.txt")

	x.ok("build", x.dir, "--clean")
	product, err = os.ReadFile(filepath.Join(x.dir, OutDir, "prod", "package", "hello_code"))
	require.NoError(t, err)
	require.Equal(t, "hello from the tree", string(product), "--clean builds HEAD")
	require.NotContains(t, e.saw[1], "services/untracked")

	// --clean of a directory with no history says why.
	y := newTester(t)
	buildable(t, y)
	r := y.run("build", y.dir, "--clean")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "--clean reads HEAD")
}

func TestWhatABuildRefuses(t *testing.T) {
	x := newTester(t)
	buildable(t, x)

	r := x.run("build", x.dir, "--env", "staging")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, `declares no environment "staging"`)

	empty := newTester(t)
	r = empty.run("build", empty.dir)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "no project here")

	// A project that declares no environments deploys nothing, as a cloud
	// refuses it.
	bare := newTester(t)
	buildable(t, bare)
	root := filepath.Join(bare.dir, "project.yaml")
	doc, err := os.ReadFile(root)
	require.NoError(t, err)
	at := bytes.Index(doc, []byte("environments:"))
	require.NoError(t, os.WriteFile(root, doc[:at], 0o644))
	r = bare.run("build", bare.dir)
	require.Equal(t, ExitBroken, r.code)
	require.Contains(t, r.stderr, "declares no environments")

	// A project that would not deploy is refused by the cloud's own audit.
	fn := filepath.Join(x.dir, "function", "hello.yaml")
	body, err := os.ReadFile(fn)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fn, bytes.Replace(body, []byte("memory: 32MB"), []byte("memory: lots"), 1), 0o644))
	r = x.run("build", x.dir)
	require.Equal(t, ExitBroken, r.code, r.stderr)
	require.Contains(t, r.stderr, "does not deploy as written")
	require.Empty(t, x.engine.(*shell).seen)
	require.NoError(t, os.WriteFile(fn, body, 0o644))

	// A build that fails is the build's error, named.
	require.NoError(t, os.Remove(filepath.Join(x.dir, "services", "shared", "greeting")))
	r = x.run("build", x.dir)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "building package/hello_code for prod")
}

func TestABranchThatFeedsNoDeployment(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	root := filepath.Join(x.dir, "project.yaml")
	body, err := os.ReadFile(root)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(root, bytes.Replace(body, []byte(`branches: ["*"]`), []byte(`branches: [main]`), 1), 0o644))

	r := x.run("build", x.dir, "--branch", "feature")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, `"feature" feeds no deployment`)
	x.ok("build", x.dir, "--env", "prod", "--branch", "feature")
}

func TestAPackageFromAnotherRepositoryIsNotBuiltHere(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	pkg := filepath.Join(x.dir, "package", "hello_code.yaml")
	body, err := os.ReadFile(pkg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(pkg, bytes.Replace(body, []byte("source: services/hello"), []byte("source:\n    git: https://github.com/acme/hello\n"), 1), 0o644))

	var out buildOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", x.dir, "-o", "json")), &out))
	require.Empty(t, out.Built)
	require.Equal(t, []string{"package/hello_code is built from https://github.com/acme/hello"}, out.Skipped)
	text := x.ok("build", x.dir)
	require.Contains(t, text, "not built here")
	require.Contains(t, text, "nothing in main's deployments is built from this directory")
}

func TestABuildIsHandedOnlyToACloudThatTakesOne(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	x.login()

	x.ok("build", x.dir)
	require.Empty(t, x.f.uploaded, "a cloud takes no build")
	require.NotContains(t, x.f.calls, "POST /upload/"+projectYAMLID(t, x.dir))

	x.f.uploads = true
	var out buildOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", x.dir, "-o", "json")), &out))
	require.Equal(t, "success", out.Status)
	require.NotEmpty(t, out.Pushed)
	require.Equal(t, []string{projectYAMLID(t, x.dir) + " upload refs/heads/main"}, x.f.queued)

	b, err := artifact.UnpackBundle(bytes.NewReader(x.f.uploaded), t.TempDir())
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(b.Tree, "project.yaml"))
	require.FileExists(t, filepath.Join(b.Tree, "services", "shared", "greeting"))
	require.NoDirExists(t, filepath.Join(b.Tree, ".tau"))
	product, ok := b.Product(out.Built[0].Recipe)
	require.True(t, ok, "the product travels under its recipe")
	product.Close()

	// The linked project id is the one it is handed under.
	x.link("proj-linked")
	x.ok("build", x.dir)
	require.Equal(t, "proj-linked upload refs/heads/main", x.f.queued[1])

	x.f.finish = buildv2.JobStatus_JobStatusFailed
	r := x.run("build", x.dir)
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "failed")
	require.Contains(t, r.stderr, "stored: built proj-linked upload", "the deploy's log is shown")
}

func TestABuildForACloudThatCannotBeAsked(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	x.login()
	x.f.fqdn = ""
	r := x.run("build", x.dir)
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stderr, "built here only")
}

func TestABuildAgainstThePublishedConfiguration(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	x.login()
	x.f.uploads = true

	r := x.run("build", x.dir, "--published")
	require.NotZero(t, r.code, "nothing is published yet")

	compiled := compiledOf(t, x)
	x.f.published[projectYAMLID(t, x.dir)+"/prod"] = []byte(strings.Replace(string(compiled), "cat ../shared/greeting", "printf published", 1))
	var out buildOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", x.dir, "--published", "-o", "json")), &out))
	product, err := os.ReadFile(filepath.Join(x.dir, out.Built[0].File))
	require.NoError(t, err)
	require.Equal(t, "published", string(product))
	require.Empty(t, x.f.queued, "and it is handed to nothing")

	x.f.published["other/prod"] = []byte("not json")
	r = x.run("build", x.dir, "--published", "--project", "other")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "reading prod's compiled configuration")

	y := newTester(t)
	buildable(t, y)
	r = y.run("build", y.dir, "--published")
	require.NotZero(t, r.code, "there is no cloud to read it from")
}

func compiledOf(t *testing.T, x *tester) []byte {
	t.Helper()
	p, err := project.Open(x.dir)
	require.NoError(t, err)
	compiled, err := p.Compiled(t.Context(), "main", "prod")
	require.NoError(t, err)
	return compiled
}

func TestABuildWhoseEngineFails(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	x.engine = failing{}
	r := x.run("build", x.dir)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "no daemon")
}

type failing struct{}

func (failing) Make(context.Context, artifact.Spec, io.Writer) (io.ReadCloser, error) {
	return nil, errors.New("no daemon")
}

func TestATreeOutsideARepository(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cache"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cache", "x"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a"), nil, 0o644))
	require.NoError(t, os.Symlink("a", filepath.Join(dir, "link")))
	files, err := walkFiles(dir)
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, files)

	_, err = walkFiles(filepath.Join(dir, "missing"))
	require.Error(t, err)
	require.Error(t, copyFiles(dir, t.TempDir(), []string{"missing"}))
	require.True(t, underTau(".tau/link.yaml"))
	require.False(t, underTau(".taurus"))
}

func TestACodeRepositoryBuildsAgainstWhatIsPublished(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	x.login()
	x.f.uploads = true
	pkg := filepath.Join(x.dir, "package", "hello_code.yaml")
	body, err := os.ReadFile(pkg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(pkg, bytes.Replace(body, []byte("source: services/hello"), []byte("source:\n    git: https://github.com/acme/lib\n    path: services/hello\n"), 1), 0o644))
	id := projectYAMLID(t, x.dir)
	x.f.published[id+"/prod"] = compiledOf(t, x)

	code := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(code, "services", "hello"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(code, "services", "shared"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(code, "services", "hello", "lib.rs"), []byte("fn main() {}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(code, "services", "shared", "greeting"), []byte("from the code repository"), 0o644))

	r := x.run("build", code)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "a code repository is built with --repo and --project")

	gitAt(t, code, "init", "-q", "-b", "main")
	gitAt(t, code, "remote", "add", "origin", "https://github.com/acme/lib.git")
	r = x.run("build", code)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "--project is required")

	var out buildOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", code, "--project", id, "-o", "json")), &out))
	require.Equal(t, "github.com/acme/lib", out.Repository)
	require.Len(t, out.Built, 1)
	product, err := os.ReadFile(filepath.Join(code, out.Built[0].File))
	require.NoError(t, err)
	require.Equal(t, "from the code repository", string(product))
	require.Equal(t, id+" upload refs/heads/main github.com/acme/lib", x.f.queued[0], "handed over as a commit of the code repository")

	// Another repository's build plans nothing here.
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", code, "--project", id, "--repo", "github.com/acme/other", "-o", "json")), &out))
	require.Empty(t, out.Built)
	require.Equal(t, []string{"package/hello_code is built from https://github.com/acme/lib"}, out.Skipped)

	// A branch the published table does not feed, and a source that is no
	// repository, are refused.
	root := filepath.Join(x.dir, "project.yaml")
	doc, err := os.ReadFile(root)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(root, bytes.Replace(doc, []byte(`branches: ["*"]`), []byte(`branches: [main]`), 1), 0o644))
	x.f.published[id+"/prod"] = compiledOf(t, x)
	r = x.run("build", code, "--project", id, "--branch", "feature")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, `"feature" feeds no deployment`)
	x.ok("build", code, "--project", id, "--branch", "feature", "--env", "prod")

	x.f.published[id+"/prod"] = []byte(strings.Replace(string(compiledOf(t, x)), "https://github.com/acme/lib", "not a url", 1))
	r = x.run("build", code, "--project", id)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "which is not a repository")
}

func TestALocalBuildReadsItsSecretsFromThisMachine(t *testing.T) {
	t.Setenv("TAU_SECRET_STRIPE_KEY", "sk_test_1")
	v := environVault{granted: []string{"stripe-key", "unset"}}

	value, err := v.Read(t.Context(), "stripe-key")
	require.NoError(t, err)
	require.Equal(t, "sk_test_1", string(value))

	_, err = v.Read(t.Context(), "unset")
	require.ErrorContains(t, err, "TAU_SECRET_UNSET is not set on this machine")

	// Set on the machine is not granted to the package.
	t.Setenv("TAU_SECRET_SLACK", "xoxb")
	_, err = v.Read(t.Context(), "slack")
	require.ErrorIs(t, err, vault.ErrNotGranted)
}
