package e2e_tests

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
)

const E2EPingProjectName = "e2e_ping"

// pingFixturePath returns the absolute path to the ping function fixture
// (tools/tau/e2e_tests/fixtures/ping).
func pingFixturePath(t *testing.T) string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return filepath.Join(root, "tools", "tau", "e2e_tests", "fixtures", "ping")
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

// copyDir copies srcDir into dstDir (dstDir must not exist or be empty for predictable result).
func copyDir(t *testing.T, srcDir, dstDir string) {
	t.Helper()
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, path)
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0644)
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
}

const pingFunctionConfigYAML = `id: QmPingE2EFixture00000000000000000000000000001
description: HTTP ping for e2e
trigger:
    type: http
    method: get
    paths:
        - /ping
domains:
    - test_domain1
source: .
execution:
    timeout: 20s
    memory: 32GB
    call: ping
`

// WithE2EPingProjectEnv creates a temp dir with a project containing the ping fixture
// (code + config), sets config and session in memory so the selected project is that
// project, and registers cleanup. Returns (dir, projectPath). Use for e2e tests that
// run tau build function + tau run function in-process.
func WithE2EPingProjectEnv(t *testing.T) (dir, projectPath string) {
	t.Helper()
	dir = t.TempDir()
	projectPath = filepath.Join(dir, "e2e_project")
	if err := os.MkdirAll(filepath.Join(projectPath, "config", "functions"), 0755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectPath, "code", "functions", "ping"), 0755); err != nil {
		t.Fatalf("mkdir code: %v", err)
	}
	copyDir(t, pingFixturePath(t), filepath.Join(projectPath, "code", "functions", "ping"))
	if err := os.WriteFile(filepath.Join(projectPath, "config", "functions", "ping.yaml"), []byte(pingFunctionConfigYAML), 0644); err != nil {
		t.Fatalf("write ping.yaml: %v", err)
	}
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

	configPath := filepath.Join(dir, "tau.yaml")
	t.Cleanup(testutil.WithConfigPath(configPath))
	configYAML := `projects:
  ` + E2EPingProjectName + `:
    location: ` + projectPath + "\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("write tau.yaml: %v", err)
	}

	config.Projects()
	if err := config.Projects().Set(E2EPingProjectName, config.Project{
		Name:           E2EPingProjectName,
		Location:       projectPath,
		DefaultProfile: "",
	}); err != nil {
		t.Fatalf("config set project: %v", err)
	}
	if err := session.LoadSessionInDir(sessionPath); err != nil {
		t.Fatalf("session load: %v", err)
	}
	if err := session.Set().SelectedProject(E2EPingProjectName); err != nil {
		t.Fatalf("session set project: %v", err)
	}
	return dir, projectPath
}
