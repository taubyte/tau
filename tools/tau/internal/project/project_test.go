package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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
	require.Empty(t, p.Environments())
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
