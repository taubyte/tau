package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/pkg/tcc/session"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func initStarter(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	written, err := Init(t.Context(), dir, "shop", false)
	require.NoError(t, err)
	require.Equal(t, starterFiles(t), written)
	return dir
}

// starterFiles is every path init writes: the four documents and the source.
func starterFiles(t *testing.T) []string {
	t.Helper()
	files := []string{"function/hello.yaml", "gateway/front.yaml", "package/hello_code.yaml", "project.yaml"}
	require.NoError(t, fs.WalkDir(source, "source", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, strings.TrimPrefix(path, "source/"))
		}
		return err
	}))
	sort.Strings(files)
	return files
}

func TestTheStarterShipsItsSource(t *testing.T) {
	dir := initStarter(t)
	for _, f := range []string{"services/hello/Cargo.toml", "services/hello/Cargo.lock", "services/hello/src/lib.rs", "wit/handler.wit"} {
		require.FileExists(t, filepath.Join(dir, f))
	}
	lib, err := os.ReadFile(filepath.Join(dir, "services", "hello", "src", "lib.rs"))
	require.NoError(t, err)
	require.Contains(t, string(lib), `path: "../../wit"`)
}

func TestTheStartersWorldIsTheHosts(t *testing.T) {
	host := filepath.Join("..", "..", "..", "..", "pkg", "invoke", "wasm", "wit")
	var want []string
	require.NoError(t, filepath.WalkDir(host, func(path string, d fs.DirEntry, err error) error {
		// The world is its WIT; the host's Go beside it (wit.go embeds it) is not.
		if err != nil || d.IsDir() || filepath.Ext(path) != ".wit" {
			return err
		}
		rel, err := filepath.Rel(host, path)
		require.NoError(t, err)
		want = append(want, filepath.ToSlash(rel))
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		copied, err := source.ReadFile("source/wit/" + filepath.ToSlash(rel))
		require.NoError(t, err, "the starter is missing %s", rel)
		require.Equal(t, string(body), string(copied), "the starter's %s differs from the host's", rel)
		return nil
	}))
	var got []string
	require.NoError(t, fs.WalkDir(source, "source/wit", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			got = append(got, strings.TrimPrefix(path, "source/wit/"))
		}
		return err
	}))
	require.Equal(t, want, got)
}

func TestInitWhereTheSourceCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "services"), []byte("a file where the directory goes"), 0o644))
	_, err := Init(t.Context(), dir, "shop", false)
	require.Error(t, err)
}

func TestTheStarterCompilesAndAuditsClean(t *testing.T) {
	dir := initStarter(t)
	p, err := Open(dir)
	require.NoError(t, err)
	require.True(t, p.IsProject())
	require.Equal(t, "shop", p.Name())

	r := p.Validate(t.Context(), "")
	require.Empty(t, r.Error)
	require.False(t, r.Broken(), "%+v", r.Findings)

	viewed, err := p.Views(t.Context(), "")
	require.NoError(t, err)
	require.NotEmpty(t, viewed.Views)
	require.Equal(t, []string{"prod"}, p.Environments())
}

func TestInitRefusesAnExistingProject(t *testing.T) {
	dir := initStarter(t)
	_, err := Init(t.Context(), dir, "shop", false)
	require.ErrorIs(t, err, ErrExists)
	_, err = Init(t.Context(), dir, "shop", true)
	require.NoError(t, err)
}

func TestInitRefusesANameTheSchemaRefuses(t *testing.T) {
	_, err := Init(t.Context(), t.TempDir(), "", false)
	require.Error(t, err)
}

func TestNewResourceFromTheSchema(t *testing.T) {
	dir := initStarter(t)
	gw := filepath.Join(dir, "gateway", "front.yaml")
	before, err := os.ReadFile(gw)
	require.NoError(t, err)

	p, err := Open(dir)
	require.NoError(t, err)
	res, issues, err := p.New("function", "bye", "", map[string]string{"package.name": "hello_code"})
	require.NoError(t, err)
	require.Equal(t, []string{"function", "bye"}, res)
	require.NotEmpty(t, issues, "a function owes its execution fields")

	for _, issue := range issues {
		require.NotEmpty(t, issue.Field)
	}
	require.Error(t, p.SetField(res, "execution.memory", "not-a-size"))
	require.Error(t, p.SetField(res, "runtime", "wasm"), "the tier is the package's, not the function's")
	require.NoError(t, p.SetField(res, "execution.call", "handle"))
	require.NoError(t, p.SetField(res, "execution.timeout", "10s"))
	require.NoError(t, p.SetField(res, "execution.memory", "16MB"))
	require.Empty(t, p.Issues(res))

	written, err := p.Save()
	require.NoError(t, err)
	require.Equal(t, []string{"function/bye.yaml"}, written)

	after, err := os.ReadFile(gw)
	require.NoError(t, err)
	require.Equal(t, before, after)

	_, _, err = p.New("function", "bye", "", nil)
	require.Error(t, err, "already exists")
	_, _, err = p.New("not-a-kind", "x", "", nil)
	require.Error(t, err)
	_, _, err = p.New("function", "x", "", map[string]string{"source": "[unclosed"})
	require.Error(t, err)
}

func TestKindsComeFromTheSchema(t *testing.T) {
	kinds := Kinds()
	require.NotEmpty(t, kinds)
	for i := 1; i < len(kinds); i++ {
		require.Less(t, kinds[i-1].Name, kinds[i].Name)
	}
}

func TestABrokenProjectReportsItsError(t *testing.T) {
	dir := initStarter(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "function", "hello.yaml"), []byte("description: no id\n"), 0o644))
	p, err := Open(dir)
	require.NoError(t, err)
	r := p.Validate(t.Context(), "")
	require.NotEmpty(t, r.Error)
	require.True(t, r.Broken())
}

func TestOpenSkipsDotDirectories(t *testing.T) {
	dir := initStarter(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tau"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".tau", "link.yaml"), []byte("project_id: x\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644))
	p, err := Open(dir)
	require.NoError(t, err)
	require.Empty(t, p.Validate(t.Context(), "").Error)

	_, err = Open(filepath.Join(dir, "missing"))
	require.Error(t, err)
}

func TestTheProjectIdIsProjectYAMLs(t *testing.T) {
	dir := t.TempDir()
	_, err := Init(t.Context(), dir, "shop", false)
	require.NoError(t, err)
	p, err := Open(dir)
	require.NoError(t, err)
	require.Regexp(t, `^Qm[1-9A-HJ-NP-Za-km-z]{44}$`, p.ID())

	empty, err := Open(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, empty.ID())
}

func TestTheStarterCompilesForItsDeployment(t *testing.T) {
	p, err := Open(initStarter(t))
	require.NoError(t, err)
	require.Equal(t, []string{"prod"}, p.Deployments("main"))

	compiled, err := p.Compiled(t.Context(), "main", "prod")
	require.NoError(t, err)
	loaded, err := specs.Load(compiled)
	require.NoError(t, err)
	require.Equal(t, p.ID(), loaded.ID())
	require.Len(t, loaded.Buildables(), 1)

	require.NoError(t, os.WriteFile(filepath.Join(p.dir, "function", "hello.yaml"), []byte("execution: {memory: lots}\n"), 0o644))
	broken, err := Open(p.dir)
	require.NoError(t, err)
	_, err = broken.Compiled(t.Context(), "main", "prod")
	require.Error(t, err, "a project that does not compile")
}

func TestAProjectWithNoEnvironmentTableFeedsNothing(t *testing.T) {
	p, err := Open(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, p.Deployments("main"))
}

func TestCheckFieldAsksWithoutSetting(t *testing.T) {
	p, err := Open(initStarter(t))
	require.NoError(t, err)
	res := []string{"function", "hello"}
	require.NoError(t, p.CheckField(res, "execution.memory", "64MB"))
	require.Error(t, p.CheckField(res, "execution.memory", "not-a-size"))
	require.Error(t, p.CheckField(res, "execution.memory", "[unclosed"))
	require.NoError(t, p.SetField(res, "description", ""), "an empty value is the empty string")
}

func TestWhatABrokenReportIs(t *testing.T) {
	require.True(t, Report{Error: "x"}.Broken())
	require.True(t, Report{Findings: []session.Note{{Severity: string(schema.Broken)}}}.Broken())
	require.False(t, Report{Findings: []session.Note{{Severity: "note"}}}.Broken())
	require.False(t, Report{}.Broken())
}

func TestValidatingWhatDoesNotCompile(t *testing.T) {
	dir := initStarter(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "function", "hello.yaml"), []byte("execution: {memory: lots}\n"), 0o644))
	p, err := Open(dir)
	require.NoError(t, err)
	r := p.Validate(t.Context(), "prod")
	require.NotEmpty(t, r.Error)
	require.True(t, r.Broken())

	empty, err := Open(t.TempDir())
	require.NoError(t, err)
	require.False(t, empty.IsProject())
	require.Empty(t, empty.Name())
	require.Empty(t, empty.ID())
	require.Empty(t, empty.Environments())
}

func TestOpeningWhatCannotBeRead(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)

	dir := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "project.yaml")))
	_, err = Open(dir)
	require.Error(t, err, "a document that cannot be read")
}

func TestSavingIntoWhatCannotBeWritten(t *testing.T) {
	dir := initStarter(t)
	p, err := Open(dir)
	require.NoError(t, err)
	_, _, err = p.New("cache", "sessions", "", nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cache"), []byte("a file where the directory goes"), 0o644))
	_, err = p.Save()
	require.Error(t, err)
}

func TestInitIntoWhatCannotBeWritten(t *testing.T) {
	parent := t.TempDir()
	blocked := filepath.Join(parent, "shop")
	require.NoError(t, os.WriteFile(blocked, []byte("a file where the directory goes"), 0o644))
	_, err := Init(t.Context(), blocked, "shop", false)
	require.Error(t, err)
}

func TestSavingOverADirectory(t *testing.T) {
	dir := initStarter(t)
	p, err := Open(dir)
	require.NoError(t, err)
	_, _, err = p.New("function", "bye", "", nil)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "function", "bye.yaml"), 0o755))
	_, err = p.Save()
	require.Error(t, err)
}

func TestAnEmptyProjectHasNoFindings(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.yaml"), []byte("id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR\nname: empty\nenvironments:\n    prod:\n        branches: [\"*\"]\n"), 0o644))
	p, err := Open(dir)
	require.NoError(t, err)
	r := p.Validate(t.Context(), "prod")
	require.Empty(t, r.Error)
	require.NotNil(t, r.Findings)
	require.Equal(t, []string{"prod"}, p.Deployments("main"))
}

func corpusProject(t *testing.T, name string) *Project {
	t.Helper()
	p, err := Open(filepath.Join("..", "..", "..", "..", "instance", "dream", "projects", filepath.FromSlash(name)))
	require.NoError(t, err)
	require.True(t, p.IsProject(), name)
	return p
}

func brokenOf(r Report) []string {
	var out []string
	for _, f := range r.Findings {
		if f.Severity == string(schema.Broken) {
			out = append(out, f.Kind+"/"+f.Name+": "+f.Title)
		}
	}
	return out
}

func TestValidateLeavesWhetherARegionIsServedToTheCloud(t *testing.T) {
	for _, name := range []string{"with/placement", "with/views"} {
		r := corpusProject(t, name).Validate(t.Context(), "prod")
		require.False(t, r.Broken(), "%s: %v", name, brokenOf(r))
	}

	across := corpusProject(t, "refused/views/across").Validate(t.Context(), "prod")
	require.Equal(t, []string{`function/reader: reaches cache/sessions_r1, which is not placed in "region-2" where this is`}, brokenOf(across))

	plain := corpusProject(t, "with/function/wasm").Validate(t.Context(), "prod")
	require.False(t, plain.Broken(), "a project naming no region is read as one region")

	missing := corpusProject(t, "refused/environments/missing").Validate(t.Context(), "prod")
	require.NotEmpty(t, missing.Error)
}
