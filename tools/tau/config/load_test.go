package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/tools/tau/constants"
)

// TestTheFileCreatedIsTheFileRead — the invariant the old `~/tau` path broke,
// silently. loadConfig creates constants.TauConfigFileName; everything that
// READS goes through Document(), which roots seer at the parent and names the
// document by the base with `.yaml` trimmed. With an extensionless path those
// are two different files: `~/tau` was created empty and `~/tau.yaml` was what
// held the config, so every user grew a stray file nothing opened.
func TestTheFileCreatedIsTheFileRead(t *testing.T) {
	dir := t.TempDir()
	restore := withConfigPath(t, filepath.Join(dir, ".tau", "config.yaml"))
	defer restore()

	require.NoError(t, loadConfig())
	require.NoError(t, Projects().Set("probe", Project{DefaultProfile: "p", Location: dir}))

	// The file loadConfig made, and nothing beside it.
	entries, err := os.ReadDir(filepath.Join(dir, ".tau"))
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.Equal(t, []string{"config.yaml"}, names,
		"a write went somewhere other than the file that was created")

	body, err := os.ReadFile(filepath.Join(dir, ".tau", "config.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(body), "probe", "the config landed in the created file")
}

// TestADirectoryWhereTheConfigGoesDoesNotPanic. `~/tau` is where people clone
// this repository, and os.Create on a directory is EISDIR — which reached the
// user as a panic out of getOrCreateConfig, so the CLI would not start at all.
// The path moved, but the loader must still REPORT rather than panic if it
// happens again.
func TestADirectoryWhereTheConfigGoesIsAnErrorNotAPanic(t *testing.T) {
	dir := t.TempDir()
	taken := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.MkdirAll(taken, 0o755))

	restore := withConfigPath(t, taken)
	defer restore()

	require.NotPanics(t, func() {
		require.Error(t, loadConfig(), "a directory in the config's place is an error")
	})
}

// TestAFirstRunMakesItsOwnDirectory — os.Create does not make parents, so a
// machine with no ~/.tau would otherwise need one made by hand before the CLI
// would start.
func TestAFirstRunMakesItsOwnDirectory(t *testing.T) {
	dir := t.TempDir()
	restore := withConfigPath(t, filepath.Join(dir, "deeper", ".tau", "config.yaml"))
	defer restore()

	require.NoError(t, loadConfig())
	require.FileExists(t, filepath.Join(dir, "deeper", ".tau", "config.yaml"))
}

// withConfigPath points the CLI at a test's own config and clears the loaded
// singleton on both sides, so one test's config never reaches another's.
func withConfigPath(t *testing.T, path string) func() {
	t.Helper()
	old := constants.TauConfigFileName
	constants.TauConfigFileName = path
	Clear()
	return func() {
		constants.TauConfigFileName = old
		Clear()
	}
}
