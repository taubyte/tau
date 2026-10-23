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

	c := exec.CommandContext(ctx, "/bin/sh", "-c", strings.Join(spec.Commands, " && "))
	c.Dir = filepath.Join(spec.Dir, spec.Path)
	c.Env = []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range spec.Variables {
		c.Env = append(c.Env, k+"="+v)
	}
	for k, v := range spec.Secrets {
		c.Env = append(c.Env, k+"="+v)
	}
	c.Stdout, c.Stderr = log, log
	if err := c.Run(); err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(spec.Dir, spec.Path, spec.Output))
}

const helloBuild = `build:
    commands:
        image: busybox:1.36
        install: mkdir -p out
        run: cat ../shared/greeting > out/hello.wasm
        output: out/hello.wasm
`

// buildable is a starter project whose function builds with the shell engine,
// reading a sibling directory the way a monorepo dependency is read.
func buildable(t *testing.T, x *tester) {
	t.Helper()
	x.ok("init", "shop", "--dir", x.dir)
	fn := filepath.Join(x.dir, "function", "hello.yaml")
	body, err := os.ReadFile(fn)
	require.NoError(t, err)
	at := bytes.Index(body, []byte("build:"))
	end := bytes.Index(body, []byte("execution:"))
	require.True(t, at > 0 && end > at)
	rewritten := append(append(append([]byte{}, body[:at]...), helloBuild...), body[end:]...)
	require.NoError(t, os.WriteFile(fn, rewritten, 0o644))
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

// A BUILD ON THIS MACHINE IS THE CLOUD'S BUILD: the plan and recipe a cloud
// reads from the compiled deployment, run in a copy of the tree, with the
// product filed by environment and resource and named by its recipe.
func TestABuildOnThisMachineIsTheCloudsBuild(t *testing.T) {
	x := newTester(t)
	buildable(t, x)

	var out buildOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", x.dir, "-o", "json")), &out))
	require.Len(t, out.Built, 1)
	b := out.Built[0]
	require.Equal(t, "prod", b.Environment)
	require.Equal(t, "function/hello", b.Kind+"/"+b.Name)
	require.Equal(t, filepath.Join(OutDir, "prod", "function", "hello"), b.File)

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
	require.Contains(t, text, "function/hello")
	require.Contains(t, text, b.Recipe[:12])
}

// THE WORKING TREE IS WHAT GIT WOULD SHIP: tracked and untracked files, not
// the ignored ones. --clean is HEAD and nothing else.
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
	product, err := os.ReadFile(filepath.Join(x.dir, OutDir, "prod", "function", "hello"))
	require.NoError(t, err)
	require.Equal(t, "uncommitted", string(product))
	e := x.engine.(*shell)
	require.Contains(t, e.saw[0], "services/untracked")
	require.NotContains(t, e.saw[0], "secret.txt")

	x.ok("build", x.dir, "--clean")
	product, err = os.ReadFile(filepath.Join(x.dir, OutDir, "prod", "function", "hello"))
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

// WHAT A BUILD REFUSES, each before anything runs.
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
	require.Contains(t, r.stderr, "building function/hello for prod")
}

// A BRANCH THAT FEEDS NOTHING builds nothing, and says to name one that does.
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

// A BUILD-TIME SECRET comes from this machine's environment, and one that is
// not set is refused by the name to set.
func TestABuildTimeSecretOnThisMachine(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	fn := filepath.Join(x.dir, "function", "hello.yaml")
	body, err := os.ReadFile(fn)
	require.NoError(t, err)
	withSecret := bytes.Replace(body, []byte("run: cat ../shared/greeting > out/hello.wasm"), []byte(`run: printf %s "$NPM_TOKEN" > out/hello.wasm`), 1)
	withSecret = append(withSecret, []byte("variables:\n    NPM_TOKEN:\n        at: build\n        secret:\n            name: npm_token\n")...)
	require.NoError(t, os.WriteFile(fn, withSecret, 0o644))
	x.ok("new", "secret", "npm_token", "--dir", x.dir, "--set", "source=value")

	t.Setenv("TAU_SECRET_NPM_TOKEN", "")
	os.Unsetenv("TAU_SECRET_NPM_TOKEN")
	r := x.run("build", x.dir)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "set TAU_SECRET_NPM_TOKEN")

	t.Setenv("TAU_SECRET_NPM_TOKEN", "s3cret")
	var out buildOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", x.dir, "-o", "json")), &out))
	product, err := os.ReadFile(filepath.Join(x.dir, out.Built[0].File))
	require.NoError(t, err)
	require.Equal(t, "s3cret", string(product))
	require.Equal(t, "S3_TOKEN_X", envName("s3-token.x"))
}

// A RESOURCE BUILT FROM ANOTHER REPOSITORY is that repository's to build.
func TestAResourceFromAnotherRepositoryIsNotBuiltHere(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	fn := filepath.Join(x.dir, "function", "hello.yaml")
	body, err := os.ReadFile(fn)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fn, bytes.Replace(body, []byte("source: services/hello"), []byte("source:\n    git: https://github.com/acme/hello\n"), 1), 0o644))

	var out buildOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("build", x.dir, "-o", "json")), &out))
	require.Empty(t, out.Built)
	require.Equal(t, []string{"function/hello is built from https://github.com/acme/hello"}, out.Skipped)
	text := x.ok("build", x.dir)
	require.Contains(t, text, "not built here")
	require.Contains(t, text, "nothing in main's deployments is built from this directory")
}

// AGAINST A CLOUD A BUILD PUBLISHES NOTHING; a development cloud is handed it
// and deploys it, and the build is as good as that deploy.
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

// A CLOUD THAT CANNOT BE ASKED leaves the build on this machine, and says so.
func TestABuildForACloudThatCannotBeAsked(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	x.login()
	x.f.fqdn = ""
	r := x.run("build", x.dir)
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stderr, "built here only")
}

// --published BUILDS AGAINST WHAT THE CLOUD RUNS, and hands nothing back.
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

// AN ENGINE THAT CANNOT RUN refuses the build by its own reason.
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

// A WALKED TREE skips dot-directories, as a repository's ignore rules would
// skip a .git.
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
