package build

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/core/builders"
	specsbuilders "github.com/taubyte/tau/legacy/specs/builders"
	ci "github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/tools/tau/tcc"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func setMockBuilder(fn func(context.Context, io.Writer, string) (builders.Builder, error)) (restore func()) {
	old := newBuilderFunc
	newBuilderFunc = fn
	return func() { newBuilderFunc = old }
}

type mockBuilder struct {
	output builders.Output
}

func (m *mockBuilder) Build(...ci.ContainerOption) (builders.Output, error) { return m.output, nil }
func (m *mockBuilder) Close() error                                         { return nil }
func (m *mockBuilder) Config() *specsbuilders.Config                        { return nil }
func (m *mockBuilder) Wd() specsbuilders.Dir                                { return &mockDir{} }
func (m *mockBuilder) Tarball() []byte                                      { return nil }

type mockDir struct{}

func (m *mockDir) Wasm() specsbuilders.Wasm               { return nil }
func (m *mockDir) Website() specsbuilders.Website         { return &mockWebsite{} }
func (m *mockDir) CodeSource(string) string               { return "" }
func (m *mockDir) TaubyteDir() string                     { return "" }
func (m *mockDir) ConfigFile() string                     { return "" }
func (m *mockDir) DockerDir() specsbuilders.DockerDirType { return nil }
func (m *mockDir) DockerFile() string                     { return "" }
func (m *mockDir) DefaultOptions(string, string, specsbuilders.Environment) []ci.ContainerOption {
	return nil
}
func (m *mockDir) SetSourceVolume() ci.ContainerOption         { return nil }
func (m *mockDir) SetOutVolume(string) ci.ContainerOption      { return nil }
func (m *mockDir) SetBuildCommand(string) ci.ContainerOption   { return nil }
func (m *mockDir) SetEnvironmentVariables() ci.ContainerOption { return nil }
func (m *mockDir) String() string                              { return "" }

type mockWebsite struct{}

func (m *mockWebsite) BuildZip() string { return "" }
func (m *mockWebsite) SetWorkDir() ci.ContainerOption {
	return func(*ci.Container) error { return nil }
}

type mockOutput struct {
	data []byte
}

func (m *mockOutput) Compress(builders.CompressionMethod) (io.ReadSeekCloser, error) {
	return &readSeekCloser{Reader: bytes.NewReader(m.data)}, nil
}
func (m *mockOutput) OutDir() string { return "" }

type readSeekCloser struct {
	*bytes.Reader
}

func (r *readSeekCloser) Close() error { return nil }

// Error-path tests (no mock)

func TestRunBuildFunction_NoProjectSelected(t *testing.T) {
	err := testutil.RunCommand(Command, "tau", "build", "function", "--name", "somefunc")
	assert.Assert(t, err != nil, "expected error when no project selected")
}

func TestRunBuildFunction_ResourceNotCloned(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(Command, "tau", "build", "function", "--name", "nonexistent_func_xyz")
	assert.Assert(t, err != nil)
}

// A kind the vocabulary does not have is not a build target.
func TestRunBuildUnknownKind(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(Command, "tau", "build", "library", "--name", "anything")
	assert.Assert(t, err != nil)
}

func TestRunBuildWebsite_NoProjectSelected(t *testing.T) {
	err := testutil.RunCommand(Command, "tau", "build", "website", "--name", "someweb")
	assert.Assert(t, err != nil)
}

func TestRunBuildWebsite_ResourceNotCloned(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(Command, "tau", "build", "website", "--name", "not_a_website")
	assert.Assert(t, err != nil)
}

func TestRunBuildLibrary_ResourceNotCloned(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(Command, "tau", "build", "library", "--name", "test_library1")
	assert.Assert(t, err != nil)
}

// Success-path tests (with mock builder)
//
// THE FIXTURE ROOT COMES FROM THE ENV HELPER AND NOT FROM A SECOND CALL. Each
// call materialises its own copy, so asking twice gave a working directory in
// one project and a selected project in another — which is why these looked
// like they were testing a build and were testing nothing.
//
// The kind and the resource are found in the fixture rather than named: the
// three that were named here were v1's, and one of them (`library`) is not a
// kind any more.

// aBuildableResource is a kind this command builds that the vocabulary still
// has and the fixture has an instance of.
//
// THE KINDS THIS COMMAND BUILDS ARE A FIXED SET, declared as its subcommands,
// and that set is where `library` still is — a kind v2 does not have. Reading
// the subcommands rather than the vocabulary is what makes the mismatch visible
// instead of a test that names one of them and breaks.
func aBuildableResource(t *testing.T) (kind, name string) {
	t.Helper()

	st, err := tcc.Open()
	assert.NilError(t, err)

	for _, sub := range Command.Subcommands {
		g, err := tcc.GroupFor(sub.Name)
		if err != nil {
			continue // a kind this command builds and the vocabulary dropped
		}
		names, err := st.List(g.Dir)
		if err != nil || len(names) == 0 {
			continue
		}
		return sub.Name, names[0]
	}

	t.Fatal("this command builds no kind the fixture has an instance of")
	return "", ""
}

func TestRunBuild_WithMockBuilder_Success(t *testing.T) {
	root := testutil.WithTCCFixtureCopyEnv(t)
	kind, name := aBuildableResource(t)

	// The build reads the resource's code out of the project's code
	// repository, so it has to be there.
	assert.NilError(t, os.MkdirAll(filepath.Join(root, "code", kind, name), 0o755))

	restore := setMockBuilder(func(context.Context, io.Writer, string) (builders.Builder, error) {
		return &mockBuilder{output: &mockOutput{data: []byte("built")}}, nil
	})
	defer restore()

	outFile := filepath.Join(t.TempDir(), "out")
	assert.NilError(t, testutil.RunCommand(Command, "tau", "build", kind, "--name", name, "-o", outFile))

	content, err := os.ReadFile(outFile)
	assert.NilError(t, err)
	assert.DeepEqual(t, content, []byte("built"))
}
