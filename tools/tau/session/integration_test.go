//go:build dreaming

package session_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"gotest.tools/v3/assert"

	commonIface "github.com/taubyte/tau/core/common"

	_ "github.com/taubyte/tau/services/auth/dream"
	_ "github.com/taubyte/tau/services/seer/dream"
)

// repoRoot returns the module root (directory containing go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/taubyte/tau")
	cmd.Dir = "."
	out, err := cmd.Output()
	assert.NilError(t, err)
	return strings.TrimSpace(string(out))
}

// buildTau builds the tau CLI into dir and returns the path to the binary.
// Uses dir/bin/tau so that dir/tau can be used as the session root (TMPDIR/tau).
func buildTau(t *testing.T, dir string) string {
	t.Helper()
	root := repoRoot(t)
	binDir := filepath.Join(dir, "bin")
	assert.NilError(t, os.MkdirAll(binDir, 0755))
	exe := filepath.Join(binDir, "tau")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, "./tools/tau")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, "go build failed: %s", out)
	return exe
}

// runTau runs the tau binary with TMPDIR and TAU_CONFIG_FILE set so session and config use dir.
func runTau(t *testing.T, tauExe, dir, configPath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	env := os.Environ()
	env = appendEnv(env, "TMPDIR", dir)
	env = appendEnv(env, "TAU_CONFIG_FILE", configPath)
	if runtime.GOOS == "windows" {
		env = appendEnv(env, "TEMP", dir)
		env = appendEnv(env, "TMP", dir)
	}
	cmd := exec.Command(tauExe, args...)
	cmd.Env = env
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	return outBuf.String(), errBuf.String(), runErr
}

func appendEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// sessionDir returns the session root directory used by tau when TMPDIR=dir (dir/tau).
func sessionDir(dir string) string {
	return filepath.Join(dir, "tau")
}

// listSessionFiles returns base names of tau-session-*.yaml in dir/tau.
func listSessionFiles(t *testing.T, dir string) []string {
	t.Helper()
	sd := sessionDir(dir)
	entries, err := os.ReadDir(sd)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		assert.NilError(t, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "tau-session-") && strings.HasSuffix(e.Name(), ".yaml") {
			names = append(names, e.Name())
		}
	}
	return names
}

// TestIntegration_SessionDiscovery_CreatesFile runs tau as a subprocess; session root is under TMPDIR.
// It verifies that running "tau current" creates a session file (discovery creates one when none exist).
func TestIntegration_SessionDiscovery_CreatesFile_Dreaming(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte("profiles: {}\nprojects: {}"), 0644))

	tauExe := buildTau(t, dir)
	_, _, err := runTau(t, tauExe, dir, configPath, "current")
	assert.NilError(t, err)

	files := listSessionFiles(t, dir)
	assert.Assert(t, len(files) >= 1, "expected at least one tau-session-*.yaml under %s/tau, got %v", dir, files)
}

// TestIntegration_SessionDiscovery_ShellWrapper runs tau directly and then via sh -c to get different PID chains.
// Both should succeed; session files may be shared (intersection) or separate depending on PIDs.
func TestIntegration_SessionDiscovery_ShellWrapper_Dreaming(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte("profiles: {}\nprojects: {}"), 0644))

	tauExe := buildTau(t, dir)

	// Run 1: direct
	stdout1, stderr1, err1 := runTau(t, tauExe, dir, configPath, "current")
	assert.NilError(t, err1, "tau current (direct) stderr: %s", stderr1)
	assert.Assert(t, strings.Contains(stdout1, "Profile") || len(stdout1) > 0, "expected current output: %s", stdout1)

	// Run 2: via shell (different parent chain: test -> sh -> tau)
	stdout2, stderr2, err2 := runTauShell(t, tauExe, dir, configPath, "current")
	assert.NilError(t, err2, "tau current (via sh) stderr: %s", stderr2)
	assert.Assert(t, strings.Contains(stdout2, "Profile") || len(stdout2) > 0, "expected current output: %s", stdout2)

	files := listSessionFiles(t, dir)
	assert.Assert(t, len(files) >= 1, "expected at least one session file, got %v", files)
}

// runTauShell runs tau via sh -c 'tau args...' so the process parent is a shell.
func runTauShell(t *testing.T, tauExe, dir, configPath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	sh := "sh"
	shArg := "-c"
	if runtime.GOOS == "windows" {
		sh = "cmd"
		shArg = "/c"
	}
	env := os.Environ()
	env = appendEnv(env, "TMPDIR", dir)
	env = appendEnv(env, "TAU_CONFIG_FILE", configPath)
	if runtime.GOOS == "windows" {
		env = appendEnv(env, "TEMP", dir)
		env = appendEnv(env, "TMP", dir)
	}
	script := tauExe + " " + strings.Join(args, " ")
	cmd := exec.Command(sh, shArg, script)
	cmd.Env = env
	cmd.Dir = dir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	return outBuf.String(), errBuf.String(), runErr
}

// startDream starts a dream multiverse and one universe (seer + auth) on the default dream port (1421).
// tau select cloud --universe sets dream_api_url in session to the default URL, so the test uses that port.
func startDream(t *testing.T) (cleanup func()) {
	t.Helper()
	// Use default port so subprocess "tau select cloud --universe" writes http://127.0.0.1:1421 to session and connects here.
	dream.DreamApiPort = 1421
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	srv, err := api.New(m, nil)
	assert.NilError(t, err)
	srv.Server().Start()
	_, err = srv.Ready(10 * time.Second)
	assert.NilError(t, err)
	universeName := "session-integration"
	u, err := m.New(dream.UniverseConfig{Name: universeName})
	assert.NilError(t, err)
	err = u.StartWithConfig(&dream.Config{
		Services: map[string]commonIface.ServiceConfig{
			"seer": {},
			"auth": {},
		},
	})
	assert.NilError(t, err)
	return func() { m.Close() }
}

// TestIntegration_SessionSetGet runs tau select cloud --universe (which sets dream_api_url in session), then tau current.
func TestIntegration_SessionSetGet_Dreaming(t *testing.T) {
	cleanup := startDream(t)
	defer cleanup()

	dir := t.TempDir()
	configYaml := `
profiles:
  test:
    provider: github
    token: "test"
    default: true
    git_username: test
    git_email: test@test.com
    type: dream
projects: {}
`
	configPath := filepath.Join(dir, "tau.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte(configYaml), 0644))

	tauExe := buildTau(t, dir)

	// tau select cloud --universe ensures default dream_api_url in session and selects the universe.
	_, stderrSet, errSet := runTau(t, tauExe, dir, configPath, "select", "cloud", "--universe", "session-integration", "--color", "never")
	assert.NilError(t, errSet, "tau select cloud stderr: %s", stderrSet)

	// tau current should show the selected cloud (dream / session-integration).
	stdout, stderr, err := runTau(t, tauExe, dir, configPath, "current", "--color", "never")
	assert.NilError(t, err, "tau current stderr: %s", stderr)
	assert.Assert(t, strings.Contains(stdout, "dream") || strings.Contains(stdout, "Dream") || strings.Contains(stdout, "session-integration"),
		"expected current to show selected dream cloud, got: %s", stdout)
}

// TestIntegration_SessionShellOfShell runs tau via sh -c 'sh -c "tau ..."' (or cmd on Windows) to simulate shell-of-shell.
func TestIntegration_SessionShellOfShell_Dreaming(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte("profiles: {}\nprojects: {}"), 0644))

	tauExe := buildTau(t, dir)

	env := os.Environ()
	env = appendEnv(env, "TMPDIR", dir)
	env = appendEnv(env, "TAU_CONFIG_FILE", configPath)
	if runtime.GOOS == "windows" {
		env = appendEnv(env, "TEMP", dir)
		env = appendEnv(env, "TMP", dir)
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Two levels: cmd -> cmd -> tau
		inner := "cmd /c \"" + tauExe + " current\""
		cmd = exec.Command("cmd", "/c", inner)
	} else {
		// Two levels: sh -> sh -> tau (escape single quotes in path for inner sh)
		escaped := strings.ReplaceAll(tauExe, "'", "'\"'\"'")
		inner := "sh -c '" + escaped + " current'"
		cmd = exec.Command("sh", "-c", inner)
	}
	cmd.Env = env
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, "tau (shell of shell) failed: %s", out)

	files := listSessionFiles(t, dir)
	assert.Assert(t, len(files) >= 1, "expected at least one session file after shell-of-shell run, got %v", files)
}

// TestIntegration_Version does not touch session; sanity check that the built binary runs.
func TestIntegration_Version_Dreaming(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte("profiles: {}\nprojects: {}"), 0644))

	tauExe := buildTau(t, dir)
	stdout, stderr, err := runTau(t, tauExe, dir, configPath, "version")
	assert.NilError(t, err, "tau version stderr: %s", stderr)
	assert.Assert(t, strings.Contains(stdout, "version") || strings.Contains(stdout, "Version") || len(stdout) > 0,
		"expected version output: %s", stdout)
}
