//go:build linux && !vagrant

package containerd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// isVagrantAvailable checks if Vagrant is available in PATH
// Works on Linux, macOS, and Windows (exec.LookPath handles .exe extension on Windows)
func isVagrantAvailable() bool {
	_, err := exec.LookPath("vagrant")
	return err == nil
}

// isVirtualBoxAvailable checks if VirtualBox is available in PATH
// Works on Linux, macOS (Intel), and Windows (exec.LookPath handles .exe extension)
// Note: VirtualBox is not supported on Apple Silicon (M1/M2/M3) Macs
func isVirtualBoxAvailable() bool {
	_, err := exec.LookPath("VBoxManage")
	return err == nil
}

// runVagrantTest runs a test inside the Vagrant VM
// rootless indicates whether to use rootless mode (true) or rootful mode (false)
func runVagrantTest(t *testing.T, testName string, rootless bool) {
	t.Helper()

	// Check if Vagrant is available
	if !isVagrantAvailable() {
		t.Skip("Skipping test: Vagrant not available")
	}

	// Check if VirtualBox is available
	if !isVirtualBoxAvailable() {
		t.Skip("Skipping test: VirtualBox not available")
	}

	mode := "rootful"
	if rootless {
		mode = "rootless"
	}

	// Get the directory containing the test file, then navigate to vagrant subdirectory
	_, filename, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(filename)
	vagrantDir := filepath.Join(testDir, "vagrant")

	// Change to vagrant directory to run vagrant commands
	originalDir, err := os.Getwd()
	require.NoError(t, err, "Should get current working directory")
	defer os.Chdir(originalDir)

	err = os.Chdir(vagrantDir)
	require.NoError(t, err, "Should change to vagrant directory")

	vmName := "tau-containerd-test"

	// Ensure VM is up
	statusCmd := exec.Command("vagrant", "status", vmName)
	statusOutput, err := statusCmd.CombinedOutput()
	vmIsRunning := err == nil && strings.Contains(string(statusOutput), "running")

	if !vmIsRunning {
		t.Logf("Starting Vagrant VM: %s", vmName)
		upCmd := exec.Command("vagrant", "up", vmName, "--provision")
		upCmd.Stdout = os.Stdout
		upCmd.Stderr = os.Stderr
		if err := upCmd.Run(); err != nil {
			t.Fatalf("Failed to start Vagrant VM: %v", err)
		}
	} else {
		// VM is running, but check if Go is installed
		// If not, reprovision
		t.Logf("VM is already running, checking if Go is installed...")
		goCheckCmd := exec.Command("vagrant", "ssh", vmName, "-c", "test -x /usr/local/go/bin/go && echo 'go-installed'")
		goCheckOutput, err := goCheckCmd.CombinedOutput()
		if err != nil || !strings.Contains(string(goCheckOutput), "go-installed") {
			t.Logf("Go not found in VM, reprovisioning...")
			provisionCmd := exec.Command("vagrant", "provision", vmName)
			provisionCmd.Stdout = os.Stdout
			provisionCmd.Stderr = os.Stderr
			if err := provisionCmd.Run(); err != nil {
				t.Fatalf("Failed to provision Vagrant VM: %v", err)
			}
		}
	}

	// Check if mode-switching script exists, if not, reprovision
	t.Logf("Checking for mode-switching script...")
	scriptCheckCmd := exec.Command("vagrant", "ssh", vmName, "-c", "test -x /usr/local/bin/switch_containerd_mode.sh && echo 'exists'")
	scriptCheckOutput, err := scriptCheckCmd.CombinedOutput()
	if err != nil || !strings.Contains(string(scriptCheckOutput), "exists") {
		t.Logf("Mode-switching script not found, reprovisioning VM...")
		provisionCmd := exec.Command("vagrant", "provision", vmName)
		provisionCmd.Stdout = os.Stdout
		provisionCmd.Stderr = os.Stderr
		if err := provisionCmd.Run(); err != nil {
			t.Fatalf("Failed to provision Vagrant VM: %v", err)
		}
	}

	// Switch containerd mode if needed
	// Run as vagrant user (not root) so rootless mode setup works correctly
	// The script will use sudo internally for systemctl commands
	t.Logf("Switching containerd to %s mode...", mode)
	switchCmd := exec.Command("vagrant", "ssh", vmName, "-c", fmt.Sprintf("/usr/local/bin/switch_containerd_mode.sh %s", mode))
	switchCmd.Stdout = os.Stdout
	switchCmd.Stderr = os.Stderr
	if err := switchCmd.Run(); err != nil {
		t.Fatalf("Failed to switch containerd to %s mode: %v", mode, err)
	}

	// Wait for containerd to be ready in the selected mode
	t.Logf("Waiting for containerd to be ready in %s mode...", mode)
	waitCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	containerdReady := false
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var socketPath string
	if rootless {
		// Rootless socket is in XDG_RUNTIME_DIR (will be evaluated in VM)
		socketPath = "/run/user/$(id -u)/tau/containerd/containerd.sock"
	} else {
		// Rootful socket is in standard location
		socketPath = "/run/containerd/containerd.sock"
	}

	for !containerdReady {
		select {
		case <-waitCtx.Done():
			// Get diagnostic info
			var diagCmd *exec.Cmd
			if rootless {
				diagCmd = exec.Command("vagrant", "ssh", vmName, "-c",
					"echo '=== Rootless Containerd Status ===' && ps aux | grep -E '(rootlesskit|containerd)' | grep -v grep || true && echo '=== Socket Check ===' && ls -la /run/user/$(id -u)/tau/containerd/containerd.sock 2>/dev/null || echo 'Socket not found'")
			} else {
				diagCmd = exec.Command("vagrant", "ssh", vmName, "-c",
					"echo '=== Containerd Status ===' && systemctl status containerd --no-pager -l || true && echo '=== Socket Check ===' && ls -la /run/containerd/containerd.sock || echo 'Socket not found'")
			}
			diagOutput, _ := diagCmd.CombinedOutput()
			t.Logf("Diagnostics:\n%s", string(diagOutput))
			t.Fatalf("Containerd not ready in %s mode after 60 seconds", mode)
		case <-ticker.C:
			// Check if containerd socket exists
			var checkCmd *exec.Cmd
			if rootless {
				// For rootless, check if socket directory exists (socket will be created by the Go code)
				// We check if XDG_RUNTIME_DIR/tau/containerd directory exists
				checkCmd = exec.Command("vagrant", "ssh", vmName, "-c",
					"test -d /run/user/$(id -u)/tau/containerd && echo 'ready' || echo 'not-ready'")
			} else {
				// For rootful, check socket and systemd service
				checkCmd = exec.Command("vagrant", "ssh", vmName, "-c",
					"test -S /run/containerd/containerd.sock && systemctl is-active --quiet containerd && echo 'ready'")
			}
			output, err := checkCmd.CombinedOutput()
			if err == nil && strings.Contains(string(output), "ready") {
				containerdReady = true
				t.Logf("Containerd is ready in %s mode", mode)
			} else {
				t.Logf("Waiting for containerd in %s mode... (output: %s)", mode, string(output))
			}
		}
	}

	// Verify Go is available in the VM
	t.Logf("Verifying Go installation in VM...")
	goCheckCmd := exec.Command("vagrant", "ssh", vmName, "-c", "export PATH=$PATH:/usr/local/go/bin && go version")
	goOutput, err := goCheckCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Go not available in VM: %v, output: %s", err, string(goOutput))
	}
	t.Logf("Go version in VM: %s", strings.TrimSpace(string(goOutput)))

	// Check containerd socket (for diagnostics)
	if mode == "rootful" {
		t.Logf("Checking containerd socket...")
		socketCheckCmd := exec.Command("vagrant", "ssh", vmName, "-c",
			"if [ -S /run/containerd/containerd.sock ]; then echo 'Socket exists'; ls -la /run/containerd/containerd.sock; else echo 'Socket does not exist'; fi")
		socketOutput, err := socketCheckCmd.CombinedOutput()
		t.Logf("Containerd socket check:\n%s", string(socketOutput))
		if err != nil {
			t.Logf("Warning: Socket check command failed: %v", err)
		}

		// Test socket accessibility as vagrant user
		t.Logf("Testing socket accessibility...")
		accessCheckCmd := exec.Command("vagrant", "ssh", vmName, "-c",
			"test -r /run/containerd/containerd.sock && test -w /run/containerd/containerd.sock && echo 'Socket is readable and writable' || echo 'Socket is NOT accessible'")
		accessOutput, err := accessCheckCmd.CombinedOutput()
		t.Logf("Socket accessibility: %s", string(accessOutput))
	} else if rootless {
		t.Logf("Rootless mode: socket will be created by the test at %s", socketPath)
	}

	// Run the test in the VM with timeout
	// Source code (repo root) is mounted at /vagrant
	testCtx, testCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer testCancel()

	// Run test from repo root in VM (/vagrant is now the repo root)
	testCmd := exec.CommandContext(testCtx, "vagrant", "ssh", vmName, "-c",
		fmt.Sprintf("cd /vagrant && export PATH=$PATH:/usr/local/go/bin && go test -tags vagrant -v -timeout 5m -run '^%s$' ./pkg/containers/backends/containerd", testName))
	testCmd.Stdout = os.Stdout
	testCmd.Stderr = os.Stderr

	t.Logf("Running test %s in VM from /vagrant in %s mode (timeout: 5 minutes)...", testName, mode)
	if err := testCmd.Run(); err != nil {
		if testCtx.Err() == context.DeadlineExceeded {
			t.Fatalf("Vagrant test %s timed out after 5 minutes", testName)
		}
		t.Fatalf("Vagrant test %s failed: %v", testName, err)
	}
	t.Logf("Test %s completed successfully", testName)
}

// TestContainerdBackend_Vagrant_All runs vagrant tests sequentially using t.Run()
// This ensures tests run in order and cleanup happens at the end
func TestContainerdBackend_Vagrant_All(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Check if Vagrant is available
	if !isVagrantAvailable() {
		t.Skip("Skipping test: Vagrant not available")
	}

	// Check if VirtualBox is available
	if !isVirtualBoxAvailable() {
		t.Skip("Skipping test: VirtualBox not available")
	}

	// Get the directory containing the test file, then navigate to vagrant subdirectory
	_, filename, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(filename)
	vagrantDir := filepath.Join(testDir, "vagrant")

	// Change to vagrant directory to run vagrant commands
	originalDir, err := os.Getwd()
	require.NoError(t, err, "Should get current working directory")
	defer os.Chdir(originalDir)

	err = os.Chdir(vagrantDir)
	require.NoError(t, err, "Should change to vagrant directory")

	vmName := "tau-containerd-test"

	// Ensure VM is up and provisioned before running tests
	statusCmd := exec.Command("vagrant", "status", vmName)
	statusOutput, err := statusCmd.CombinedOutput()
	vmIsRunning := err == nil && strings.Contains(string(statusOutput), "running")

	if !vmIsRunning {
		t.Logf("Starting Vagrant VM: %s", vmName)
		upCmd := exec.Command("vagrant", "up", vmName, "--provision")
		upCmd.Stdout = os.Stdout
		upCmd.Stderr = os.Stderr
		if err := upCmd.Run(); err != nil {
			t.Fatalf("Failed to start Vagrant VM: %v", err)
		}
	}

	// Cleanup function to halt VM at the end
	defer func() {
		t.Logf("Cleaning up: halting Vagrant VM...")
		haltCmd := exec.Command("vagrant", "halt", vmName)
		haltCmd.Stdout = os.Stdout
		haltCmd.Stderr = os.Stderr
		if err := haltCmd.Run(); err != nil {
			t.Logf("Warning: Failed to halt VM: %v", err)
		} else {
			t.Logf("VM halted successfully")
		}
	}()

	// Run tests sequentially
	t.Run("RootfulMode", func(t *testing.T) {
		runVagrantTest(t, "TestContainerdBackend_Vagrant_RootfulMode", false)
	})

	t.Run("ContainerOperations", func(t *testing.T) {
		runVagrantTest(t, "TestContainerdBackend_Vagrant_ContainerOperations", false)
	})

	t.Run("RootlessMode", func(t *testing.T) {
		runVagrantTest(t, "TestContainerdBackend_Vagrant_RootlessMode", true)
	})

	t.Run("RootlessContainerOperations", func(t *testing.T) {
		runVagrantTest(t, "TestContainerdBackend_Vagrant_RootlessContainerOperations", true)
	})
}
