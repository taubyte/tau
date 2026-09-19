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
		"pkg/tcc/taubyte/v2/config.schema.json",
		"pkg/tcc/taubyte/v2/explorer/src/gen/schema.ts",
	} {
		require.Contains(t, out, "OK    "+filepath.FromSlash(rel))
	}
	require.Contains(t, out, "4 generated files checked, 0 with differences")
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

	require.Equal(t, 4, strings.Count(report.String(), "DIFF  "))
	require.Contains(t, report.String(), "4 generated files checked, 4 with differences")
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
