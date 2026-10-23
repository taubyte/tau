package project

import (
	"os"
	"path/filepath"
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
	require.Equal(t, []string{"function/hello.yaml", "gateway/front.yaml", "project.yaml"}, written)
	return dir
}

// The starter compiles clean — the whole reason init compiles before writing.
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

// A new resource gets a minted id, reports what it owes, and is written only
// once asked; untouched documents are not rewritten.
func TestNewResourceFromTheSchema(t *testing.T) {
	dir := initStarter(t)
	gw := filepath.Join(dir, "gateway", "front.yaml")
	before, err := os.ReadFile(gw)
	require.NoError(t, err)

	p, err := Open(dir)
	require.NoError(t, err)
	res, issues, err := p.New("function", "bye", "", map[string]string{"source": "services/bye"})
	require.NoError(t, err)
	require.Equal(t, []string{"function", "bye"}, res)
	require.NotEmpty(t, issues, "a function owes its execution fields")

	for _, issue := range issues {
		require.NotEmpty(t, issue.Field)
	}
	require.Error(t, p.SetField(res, "execution.memory", "not-a-size"))
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

// A DEPLOYMENT COMPILES AS A CLOUD'S DEPLOY COMPILES IT: the branch picks the
// deployments, and the bytes are the compiled object a register is published.
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

// A value is checked against the field's rule without being set.
func TestCheckFieldAsksWithoutSetting(t *testing.T) {
	p, err := Open(initStarter(t))
	require.NoError(t, err)
	res := []string{"function", "hello"}
	require.NoError(t, p.CheckField(res, "execution.memory", "64MB"))
	require.Error(t, p.CheckField(res, "execution.memory", "not-a-size"))
	require.Error(t, p.CheckField(res, "execution.memory", "[unclosed"))
	require.NoError(t, p.SetField(res, "description", ""), "an empty value is the empty string")
}

// A REPORT IS BROKEN BY A COMPILE ERROR OR A BROKEN FINDING, and by nothing
// milder.
func TestWhatABrokenReportIs(t *testing.T) {
	require.True(t, Report{Error: "x"}.Broken())
	require.True(t, Report{Findings: []session.Note{{Severity: string(schema.Broken)}}}.Broken())
	require.False(t, Report{Findings: []session.Note{{Severity: "note"}}}.Broken())
	require.False(t, Report{}.Broken())
}

// A PROJECT THAT DOES NOT COMPILE is reported as one, and a directory that is
// not a project answers nothing.
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
