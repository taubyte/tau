package build

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/core/builders"
	ci "github.com/taubyte/tau/pkg/containers"
	specsbuilders "github.com/taubyte/tau/pkg/specs/builders"
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

func TestRunBuildWebsite_NoProjectSelected(t *testing.T) {
	err := testutil.RunCommand(Command, "tau", "build", "website", "--name", "someweb")
	assert.Assert(t, err != nil)
}

func TestRunBuildLibrary_NoProjectSelected(t *testing.T) {
	err := testutil.RunCommand(Command, "tau", "build", "library", "--name", "somelib")
	assert.Assert(t, err != nil)
}

func TestRunBuildWebsite_ResourceNotCloned(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(Command, "tau", "build", "website", "--name", "test_website1")
	assert.Assert(t, err != nil)
}

func TestRunBuildLibrary_ResourceNotCloned(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := testutil.RunCommand(Command, "tau", "build", "library", "--name", "test_library1")
	assert.Assert(t, err != nil)
}

// Success-path tests (with mock builder)

func TestRunBuildFunction_WithMockBuilder_Success(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	fixtureRoot, err := testutil.TCCFixtureProjectRoot()
	assert.NilError(t, err)
	workDir := filepath.Join(fixtureRoot, "code", "functions", "test_function1_glob")
	assert.NilError(t, os.MkdirAll(workDir, 0755))
	defer os.RemoveAll(filepath.Join(fixtureRoot, "code"))

	restore := setMockBuilder(func(context.Context, io.Writer, string) (builders.Builder, error) {
		return &mockBuilder{output: &mockOutput{data: []byte("wasm")}}, nil
	})
	defer restore()

	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.wasm")
	err = testutil.RunCommand(Command, "tau", "build", "function", "--name", "test_function1_glob", "-o", outFile)
	assert.NilError(t, err)
	content, err := os.ReadFile(outFile)
	assert.NilError(t, err)
	assert.DeepEqual(t, content, []byte("wasm"))
}

func TestRunBuildWebsite_WithMockBuilder_Success(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	fixtureRoot, err := testutil.TCCFixtureProjectRoot()
	assert.NilError(t, err)
	workDir := filepath.Join(fixtureRoot, "websites", "photo_booth")
	assert.NilError(t, os.MkdirAll(workDir, 0755))
	defer os.RemoveAll(filepath.Join(fixtureRoot, "websites"))

	restore := setMockBuilder(func(context.Context, io.Writer, string) (builders.Builder, error) {
		return &mockBuilder{output: &mockOutput{data: []byte("zip")}}, nil
	})
	defer restore()

	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.zip")
	err = testutil.RunCommand(Command, "tau", "build", "website", "--name", "test_website1", "-o", outFile)
	assert.NilError(t, err)
	content, err := os.ReadFile(outFile)
	assert.NilError(t, err)
	assert.DeepEqual(t, content, []byte("zip"))
}

func TestRunBuildLibrary_WithMockBuilder_Success(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	fixtureRoot, err := testutil.TCCFixtureProjectRoot()
	assert.NilError(t, err)
	workDir := filepath.Join(fixtureRoot, "libraries", "library1")
	assert.NilError(t, os.MkdirAll(workDir, 0755))
	defer os.RemoveAll(filepath.Join(fixtureRoot, "libraries"))

	restore := setMockBuilder(func(context.Context, io.Writer, string) (builders.Builder, error) {
		return &mockBuilder{output: &mockOutput{data: []byte("libwasm")}}, nil
	})
	defer restore()

	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.wasm")
	err = testutil.RunCommand(Command, "tau", "build", "library", "--name", "test_library1", "-o", outFile)
	assert.NilError(t, err)
	content, err := os.ReadFile(outFile)
	assert.NilError(t, err)
	assert.DeepEqual(t, content, []byte("libwasm"))
}
