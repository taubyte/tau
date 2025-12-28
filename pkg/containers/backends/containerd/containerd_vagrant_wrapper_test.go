//go:build !vagrant

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
func isVagrantAvailable() bool {
	_, err := exec.LookPath("vagrant")
	return err == nil
}

// runVagrantTest runs a test inside the Vagrant VM
func runVagrantTest(t *testing.T, testName string) {
	t.Helper()

	// Check if Vagrant is available
	if !isVagrantAvailable() {
		t.Skip("Skipping test: Vagrant not available")
	}

	// Get the directory containing the test file (where Vagrantfile is)
	_, filename, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(filename)

	// Change to test directory to run vagrant commands
	originalDir, err := os.Getwd()
	require.NoError(t, err, "Should get current working directory")
	defer os.Chdir(originalDir)

	err = os.Chdir(testDir)
	require.NoError(t, err, "Should change to test directory")

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

	// Wait for containerd to be ready in the VM before running tests
	t.Logf("Waiting for containerd to be ready in VM...")
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	containerdReady := false
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for !containerdReady {
		select {
		case <-waitCtx.Done():
			// Get diagnostic info
			diagCmd := exec.Command("vagrant", "ssh", vmName, "-c",
				"echo '=== Containerd Status ===' && systemctl status containerd --no-pager -l || true && echo '=== Socket Check ===' && ls -la /run/containerd/containerd.sock || echo 'Socket not found'")
			diagOutput, _ := diagCmd.CombinedOutput()
			t.Logf("Diagnostics:\n%s", string(diagOutput))
			t.Fatalf("Containerd not ready in VM after 30 seconds")
		case <-ticker.C:
			// Check if containerd socket exists and service is running
			checkCmd := exec.Command("vagrant", "ssh", vmName, "-c",
				"test -S /run/containerd/containerd.sock && systemctl is-active --quiet containerd && echo 'ready'")
			output, err := checkCmd.CombinedOutput()
			if err == nil && strings.Contains(string(output), "ready") {
				containerdReady = true
				t.Logf("Containerd is ready in VM")
			} else {
				t.Logf("Waiting for containerd... (socket check: %v, output: %s)", err == nil, string(output))
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

	// Check containerd socket permissions and accessibility
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

	// Run the test in the VM with timeout
	// Source code (repo root) is mounted at /vagrant
	testCtx, testCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer testCancel()

	// Run test from repo root in VM (/vagrant is now the repo root)
	// Socket permissions are set to 666 so vagrant user can access it
	testCmd := exec.CommandContext(testCtx, "vagrant", "ssh", vmName, "-c",
		fmt.Sprintf("cd /vagrant && export PATH=$PATH:/usr/local/go/bin && go test -tags vagrant -v -timeout 5m -run '^%s$' ./pkg/containers/backends/containerd", testName))
	testCmd.Stdout = os.Stdout
	testCmd.Stderr = os.Stderr

	t.Logf("Running test %s in VM from /vagrant (repo root, timeout: 5 minutes)...", testName)
	if err := testCmd.Run(); err != nil {
		if testCtx.Err() == context.DeadlineExceeded {
			t.Fatalf("Vagrant test %s timed out after 5 minutes", testName)
		}
		t.Fatalf("Vagrant test %s failed: %v", testName, err)
	}
	t.Logf("Test %s completed successfully", testName)
}

// TestContainerdBackend_Vagrant_RootfulMode is a wrapper that runs the test inside the Vagrant VM
// The actual test implementation is in containerd_vagrant_test.go with vagrant build tag
func TestContainerdBackend_Vagrant_RootfulMode(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	runVagrantTest(t, "TestContainerdBackend_Vagrant_RootfulMode")
}

// TestContainerdBackend_Vagrant_ContainerOperations is a wrapper that runs the test inside the Vagrant VM
// The actual test implementation is in containerd_vagrant_test.go with vagrant build tag
func TestContainerdBackend_Vagrant_ContainerOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	runVagrantTest(t, "TestContainerdBackend_Vagrant_ContainerOperations")
}
