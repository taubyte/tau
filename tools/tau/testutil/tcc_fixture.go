package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/session"
)

// TCCFixtureSource is the project every CLI test reads: the vocabulary's own
// fixture.
//
// IT IS THE SCHEMA'S FIXTURE AND NOT ONE OF THE CLI'S OWN, and that is the
// whole point. The previous one lived under pkg/tcc/taubyte/v1/fixtures and
// went with v1 when v1 was deleted — so every store, build, validate and
// resource test in this tool broke at once, silently, because the copy of a
// directory that does not exist succeeds and produces an empty project.
//
// Pointing at the schema's fixture means the CLI is tested against whatever the
// vocabulary currently says, which is the same rule the rest of this tool
// follows: the DSL is the source of truth.
func TCCFixtureSource() (string, error) {
	root, err := findRepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "pkg", "tcc", "taubyte", "v2", "fixtures", "basic"), nil
}

// TCCFixtureProjectRoot materialises the fixture as a PROJECT: a directory with
// the config repo at <returned path>/config, which is the layout every CLI path
// expects.
//
// The schema's fixture is a bare config tree — it is what a config repository
// contains, not a checkout of one — so the layout is built here rather than
// duplicated on disk.
func TCCFixtureProjectRoot() (string, error) {
	src, err := TCCFixtureSource()
	if err != nil {
		return "", err
	}

	root, err := os.MkdirTemp("", "tau-fixture-")
	if err != nil {
		return "", err
	}
	project := filepath.Join(root, "project")
	if err := os.CopyFS(filepath.Join(project, "config"), os.DirFS(src)); err != nil {
		return "", err
	}
	return project, nil
}

const TCCFixtureProjectName = "fixture"

// WithTCCFixtureEnv sets up config and session so that the selected project is the TCC
// fixture project (config + session in a temp dir). Restores config path and clears
// config/session on cleanup. Use for lib tests that call projectLib.SelectedProjectInterface()
// or applicationLib.List(), etc.
func WithTCCFixtureEnv(t *testing.T) {
	t.Helper()
	WithTCCFixtureCopyEnv(t)
}

// WithTCCFixtureCopyEnv is WithTCCFixtureEnv over a throwaway copy of the
// fixtures, for tests that write to the project (new/edit/delete). Returns the
// copy's root.
func WithTCCFixtureCopyEnv(t *testing.T) string {
	t.Helper()

	src, err := TCCFixtureSource()
	if err != nil {
		t.Fatalf("TCC fixture path: %v", err)
	}

	root := filepath.Join(t.TempDir(), "project")
	if err := os.CopyFS(filepath.Join(root, "config"), os.DirFS(src)); err != nil {
		t.Fatalf("copying fixtures: %v", err)
	}

	// A FIXTURE THAT COPIED NOTHING IS AN EMPTY PROJECT, and every assertion
	// against it fails somewhere far from the cause. CopyFS over a missing
	// source is not an error, which is how the last one went unnoticed.
	entries, err := os.ReadDir(filepath.Join(root, "config"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("the fixture at %s is empty; nothing was copied", src)
	}

	withFixtureEnv(t, root)
	return root
}

func withFixtureEnv(t *testing.T, fixtureRoot string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	sessionPath := filepath.Join(dir, "session")
	if err := os.MkdirAll(sessionPath, 0755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}

	config.Clear()
	session.Clear()
	t.Cleanup(func() {
		config.Clear()
		session.Clear()
	})

	restoreConfig := WithConfigPath(configPath)
	t.Cleanup(restoreConfig)

	config.Projects()
	if err := config.Projects().Set(TCCFixtureProjectName, config.Project{
		Name:           TCCFixtureProjectName,
		Location:       fixtureRoot,
		DefaultProfile: "",
	}); err != nil {
		t.Fatalf("config set project: %v", err)
	}
	if err := session.LoadSessionInDir(sessionPath); err != nil {
		t.Fatalf("session load: %v", err)
	}
	if err := session.Set().SelectedProject(TCCFixtureProjectName); err != nil {
		t.Fatalf("session set project: %v", err)
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
