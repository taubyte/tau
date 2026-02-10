package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/session"
)

// TCCFixtureProjectRoot returns the absolute path to the TCC fixture project root
// (pkg/tcc/taubyte/v1/fixtures). The project config repo is at <returned path>/config.
// Use this with config.Project{Location: TCCFixtureProjectRoot()} so that Interface()
// opens the schema project from the TCC fixtures.
func TCCFixtureProjectRoot() (string, error) {
	root, err := findRepoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "pkg", "tcc", "taubyte", "v1", "fixtures"), nil
}

const TCCFixtureProjectName = "fixture"

// WithTCCFixtureEnv sets up config and session so that the selected project is the TCC
// fixture project (config + session in a temp dir). Restores config path and clears
// config/session on cleanup. Use for lib tests that call projectLib.SelectedProjectInterface()
// or applicationLib.List(), etc.
func WithTCCFixtureEnv(t *testing.T) {
	t.Helper()
	fixtureRoot, err := TCCFixtureProjectRoot()
	if err != nil {
		t.Fatalf("TCC fixture path: %v", err)
	}
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
