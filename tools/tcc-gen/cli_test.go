package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// cli_test drives the command the way a person does, because the parts it
// exercises — writing the files out, and reading them back to report drift —
// have no other caller. TestNoDrift beside it asserts the CONTENT; this asserts
// that the command puts it where the drift check looks.

func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	root, err := findRepoRoot(cwd)
	require.NoError(t, err)
	return root
}

func TestTheCommandWritesEveryGeneratedFileWhereItBelongs(t *testing.T) {
	out := t.TempDir()
	root := repoRoot(t)

	require.NoError(t, newApp().Run([]string{"tcc-gen", "--root", root, "--out", out}))

	// EVERY FILE, AT ITS REPO PATH. The output dir mirrors the tree so adopting
	// is a copy and never a decision about where something goes.
	var written []string
	require.NoError(t, filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(out, p)
		if err != nil {
			return err
		}
		written = append(written, filepath.ToSlash(rel))
		return nil
	}))
	require.ElementsMatch(t, []string{
		"pkg/specs/kinds.go",
		"pkg/specs/project.go",
		"pkg/tcc/clients/js/src/gen/schema.ts",
		"pkg/tcc/taubyte/v2/config.schema.json",
		"pkg/tcc/taubyte/v2/explorer/src/gen/schema.ts",
	}, written)

	// AND WHAT WAS WRITTEN IS WHAT IS COMMITTED, which is the same claim
	// --check makes and the reason this can be adopted with cp.
	for _, rel := range written {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
		require.NoError(t, err)
		want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		require.NoError(t, err)
		require.True(t, bytes.Equal(got, want), "%s differs from the committed copy", rel)
	}
}

func TestTheDriftReportNamesEveryFileItChecked(t *testing.T) {
	root := repoRoot(t)

	// --check prints to stdout, so it is captured rather than returned.
	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	runErr := newApp().Run([]string{"tcc-gen", "--root", root, "--check"})
	w.Close()
	os.Stdout = stdout

	var report bytes.Buffer
	_, err = report.ReadFrom(r)
	require.NoError(t, err)
	require.NoError(t, runErr)

	out := report.String()
	for _, rel := range []string{
		"pkg/specs/kinds.go",
		"pkg/specs/project.go",
		"pkg/tcc/clients/js/src/gen/schema.ts",
		"pkg/tcc/taubyte/v2/config.schema.json",
		"pkg/tcc/taubyte/v2/explorer/src/gen/schema.ts",
	} {
		require.Contains(t, out, "OK    "+filepath.FromSlash(rel))
	}
	require.Contains(t, out, "5 generated files checked, 0 with differences")
	require.NotContains(t, out, "DIFF")
}

func TestAFileThatIsNotThereIsDrift(t *testing.T) {
	// A tree with none of the generated files in it: every one of them is
	// missing, which reports the same way a changed one does — there is nothing
	// on disk that says what the vocabulary now says.
	empty := t.TempDir()

	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	runErr := newApp().Run([]string{"tcc-gen", "--root", empty, "--check"})
	w.Close()
	os.Stdout = stdout

	var report bytes.Buffer
	_, err = report.ReadFrom(r)
	require.NoError(t, err)
	require.NoError(t, runErr)

	require.Equal(t, 5, strings.Count(report.String(), "DIFF  "))
	require.Contains(t, report.String(), "5 generated files checked, 5 with differences")
}

func TestAVocabularyThatIsNotOneIsRefused(t *testing.T) {
	err := newApp().Run([]string{"tcc-gen", "--root", repoRoot(t), "--dsl", "v3", "--out", t.TempDir()})
	require.ErrorContains(t, err, "v3")
}

// TestTheTypeScriptIsGeneratedByTheSameCommand. The explorer's schema.ts comes
// out of this tool too, and `--ts` writes it on its own — the vocabulary's own
// directory by default, anywhere with --out. It is the half that leaves the Go
// tree entirely, which is exactly how it gets forgotten.
func TestTheTypeScriptIsGeneratedByTheSameCommand(t *testing.T) {
	out := t.TempDir()
	root := repoRoot(t)

	require.NoError(t, newApp().Run([]string{"tcc-gen", "--root", root, "--ts", "--out", out}))

	got, err := os.ReadFile(filepath.Join(out, "schema.ts"))
	require.NoError(t, err)

	// AND IT IS THE COMMITTED ONE. --ts and a full generation are two ways to
	// the same file; if they disagreed, whichever was run last would win and
	// the drift check would flip with it.
	want, err := os.ReadFile(filepath.Join(root, "pkg/tcc/taubyte/v2/explorer/src/gen/schema.ts"))
	require.NoError(t, err)
	require.True(t, bytes.Equal(got, want), "--ts wrote something other than the committed schema.ts")
}

// WITH NO --root THE REPOSITORY IS FOUND FROM WHERE THE COMMAND RUNS, which is
// how a person runs it; outside any checkout of tau there is nothing to find,
// and it says so.
func TestTheRootIsFoundFromWhereTheCommandRuns(t *testing.T) {
	stdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	runErr := newApp().Run([]string{"tcc-gen", "--check"})
	w.Close()
	os.Stdout = stdout

	var report bytes.Buffer
	_, err = report.ReadFrom(r)
	require.NoError(t, err)
	require.NoError(t, runErr)
	require.Contains(t, report.String(), "5 generated files checked, 0 with differences")

	t.Chdir(t.TempDir())
	require.ErrorContains(t, newApp().Run([]string{"tcc-gen", "--check"}), "could not locate tau module root")
}

// WITH NO --out EACH MODE WRITES WHERE ITS OWN DEFAULT IS: a full generation
// into a fresh temporary directory, and --ts into the vocabulary's directory
// under the root.
func TestEachModesDefaultOutput(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	require.NoError(t, newApp().Run([]string{"tcc-gen", "--root", repoRoot(t)}))
	written, err := filepath.Glob(filepath.Join(tmp, "tcc-gen-*", "pkg", "specs", "kinds.go"))
	require.NoError(t, err)
	require.Len(t, written, 1)

	root := t.TempDir()
	require.NoError(t, newApp().Run([]string{"tcc-gen", "--root", root, "--ts"}))
	require.FileExists(t, filepath.Join(root, "pkg", "tcc", "taubyte", "v2", "explorer", "src", "gen", "schema.ts"))
}

// WHAT CANNOT BE WRITTEN IS AN ERROR, in every mode, and so is a wasm build
// that fails.
func TestWhatTheCommandCannotDoIsAnError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	root := repoRoot(t)

	for _, mode := range [][]string{nil, {"--ts"}, {"--wasm"}} {
		args := append([]string{"tcc-gen", "--root", root, "--out", filepath.Join(file, "under")}, mode...)
		require.Error(t, newApp().Run(args), "%v", mode)
	}

	err := newApp().Run([]string{"tcc-gen", "--root", t.TempDir(), "--wasm"})
	require.ErrorContains(t, err, "building tcc.wasm failed")
}

// MAIN IS THE COMMAND, run with the arguments a person types.
func TestMainRunsTheCommand(t *testing.T) {
	args := os.Args
	t.Cleanup(func() { os.Args = args })
	os.Args = []string{"tcc-gen", "--root", repoRoot(t), "--check"}
	main()
}

// A FILE THE CHECK CANNOT READ IS AN ERROR, not drift: a directory where a
// generated file belongs says nothing about what the vocabulary generates.
func TestACheckThatCannotReadAFileIsAnError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg", "specs", "kinds.go"), 0o755))
	require.Error(t, newApp().Run([]string{"tcc-gen", "--root", root, "--check"}))
}

// NOWHERE TO WRITE IS AN ERROR: no temporary directory for a full generation,
// and a directory where schema.ts goes.
func TestNowhereToWriteIsAnError(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	require.Error(t, newApp().Run([]string{"tcc-gen", "--root", repoRoot(t)}))

	out := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(out, "schema.ts"), 0o755))
	require.Error(t, newApp().Run([]string{"tcc-gen", "--root", repoRoot(t), "--ts", "--out", out}))
}

// THE WASM MODULE IS BUILT WITH ITS LOADER BESIDE IT, into the vocabulary's own
// directory by default. The package built is whatever the root holds at the
// vocabulary's wasm path, so a one-line main stands in for the real one here
// and the build costs nothing to speak of.
func TestTheWasmModuleIsBuiltWithItsLoader(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wasmonly\n\ngo 1.22\n"), 0o644))
	pkg := filepath.Join(root, "pkg", "tcc", "taubyte", "v2", "wasm")
	require.NoError(t, os.MkdirAll(pkg, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))

	require.NoError(t, newApp().Run([]string{"tcc-gen", "--root", root, "--wasm"}))
	public := filepath.Join(root, "pkg", "tcc", "taubyte", "v2", "explorer", "public")
	require.FileExists(t, filepath.Join(public, "tcc.wasm"))
	require.FileExists(t, filepath.Join(public, "wasm_exec.js"))

	blocked := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(blocked, "wasm_exec.js"), 0o755))
	err := newApp().Run([]string{"tcc-gen", "--root", root, "--wasm", "--out", blocked})
	require.ErrorContains(t, err, "copying wasm_exec.js failed")
}
