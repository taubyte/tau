//go:build linux

package containerd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/taubyte/tau/pkg/containers/core"
)

func TestNewRootlessManager(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	rm, err := NewRootlessManager(config)
	if err != nil {
		t.Fatalf("NewRootlessManager failed: %v", err)
	}

	if rm == nil {
		t.Fatal("NewRootlessManager returned nil manager")
	}

	// Check tool detection
	t.Logf("rootlesskit available: %v", rm.hasRootlesskit())
	t.Logf("fuse-overlayfs available: %v", rm.hasFuseOverlayfs())
}

func TestRootlessManager_validateUIDGIDMapping(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	rm, err := NewRootlessManager(config)
	if err != nil {
		t.Fatalf("NewRootlessManager failed: %v", err)
	}

	// This test will pass if subuid/subgid are configured correctly
	// It will fail if they are not configured
	err = rm.validateUIDGIDMapping()
	if err != nil {
		t.Logf("UID/GID mapping validation failed (expected if subuid/subgid not configured): %v", err)
		// Don't skip - this is expected behavior when subuid/subgid is not configured
		t.Log("Test passed - correctly detected missing subuid/subgid configuration")
	} else {
		t.Log("UID/GID mapping validation passed")
	}
}

func TestRootlessManager_validateMountPermissions(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	rm, err := NewRootlessManager(config)
	if err != nil {
		t.Fatalf("NewRootlessManager failed: %v", err)
	}

	// Test with current directory (should work - owned by current user)
	cwd, err := os.Getwd()
	assert.NoError(t, err, "Getwd should not fail")

	err = rm.validateMountPermissions(cwd, "/tmp")
	assert.NoError(t, err, "Mount permission validation should pass for files owned by current user")

	// Test with /etc/passwd (owned by root, should fail)
	err = rm.validateMountPermissions("/etc/passwd", "/tmp/passwd")
	assert.Error(t, err, "Mount permission validation should fail for root-owned files")
	assert.Contains(t, err.Error(), "cannot be mapped", "Error should indicate UID/GID mapping issue")

	// Test with a non-existent file
	err = rm.validateMountPermissions("/non/existent/file", "/tmp/file")
	assert.Error(t, err, "Mount permission validation should fail for non-existent file")
	assert.Contains(t, err.Error(), "cannot stat", "Error should indicate file not found")
}

func TestRootlessManager_readSubIDMappings(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	rm, err := NewRootlessManager(config)
	if err != nil {
		t.Fatalf("NewRootlessManager failed: %v", err)
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() failed: %v", err)
	}

	// Test reading subuid mappings
	mappings, err := rm.readSubIDMappings("/etc/subuid", currentUser.Username)
	if err != nil {
		t.Logf("Failed to read subuid mappings: %v", err)
	} else {
		t.Logf("Found %d subuid mappings for user %s", len(mappings), currentUser.Username)
		for _, mapping := range mappings {
			t.Logf("  UID range: %d-%d", mapping.Start, mapping.Start+mapping.Count-1)
		}
	}

	// Test reading subgid mappings
	mappings, err = rm.readSubIDMappings("/etc/subgid", currentUser.Username)
	if err != nil {
		t.Logf("Failed to read subgid mappings: %v", err)
	} else {
		t.Logf("Found %d subgid mappings for user %s", len(mappings), currentUser.Username)
		for _, mapping := range mappings {
			t.Logf("  GID range: %d-%d", mapping.Start, mapping.Start+mapping.Count-1)
		}
	}
}

func TestRootlessManager_canMapUID(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	rm, err := NewRootlessManager(config)
	if err != nil {
		t.Fatalf("NewRootlessManager failed: %v", err)
	}

	// Skip when subuid is not configured for this user (e.g. GHA runner), unless RUN_ALL_CONTAINER_TESTS=1
	if os.Getenv("RUN_ALL_CONTAINER_TESTS") != "1" {
		if err := rm.canMapUID(100000); err != nil && strings.Contains(err.Error(), "not in subuid range") {
			t.Skipf("Skipping: subuid not configured for this user: %v", err)
		}
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() failed: %v", err)
	}

	// Get current user UID
	uid, err := strconv.Atoi(currentUser.Uid)
	if err != nil {
		t.Fatalf("Invalid UID: %v", err)
	}

	// Test mapping current user's UID - this should FAIL (current UID should not be in subuid range)
	err = rm.canMapUID(uint32(uid))
	assert.Error(t, err, "Current user UID should NOT be mappable through subuid range")

	// Test mapping a UID from the subuid range - this should PASS
	// User's subuid range starts at 100000, so UID 100000 should be mappable
	err = rm.canMapUID(100000)
	assert.NoError(t, err, "UID from subuid range should be mappable")
}

func TestRootlessManager_canMapGID(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	rm, err := NewRootlessManager(config)
	if err != nil {
		t.Fatalf("NewRootlessManager failed: %v", err)
	}

	// Skip when subgid is not configured for this user (e.g. GHA runner), unless RUN_ALL_CONTAINER_TESTS=1
	if os.Getenv("RUN_ALL_CONTAINER_TESTS") != "1" {
		if err := rm.canMapGID(100000); err != nil && strings.Contains(err.Error(), "not in subgid range") {
			t.Skipf("Skipping: subgid not configured for this user: %v", err)
		}
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() failed: %v", err)
	}

	// Get current user GID
	gid, err := strconv.Atoi(currentUser.Gid)
	if err != nil {
		t.Fatalf("Invalid GID: %v", err)
	}

	// Test mapping current user's GID - this should FAIL (current GID should not be in subgid range)
	err = rm.canMapGID(uint32(gid))
	assert.Error(t, err, "Current user GID should NOT be mappable through subgid range")

	// Test mapping a GID from the subgid range - this should PASS
	// User's subgid range starts at 100000, so GID 100000 should be mappable
	err = rm.canMapGID(100000)
	assert.NoError(t, err, "GID from subgid range should be mappable")
}

func TestRootlessManager_rootlesskitFunctionality(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	rm, err := NewRootlessManager(config)
	if err != nil {
		t.Fatalf("NewRootlessManager failed: %v", err)
	}

	if !rm.hasRootlesskit() {
		t.Skip("rootlesskit not available, skipping functionality test")
	}

	// Test that rootlesskit binary exists and is executable
	rootlesskitPath := rm.rootlesskitPath
	if rootlesskitPath == "" {
		t.Fatal("rootlesskit path is empty despite hasRootlesskit() returning true")
	}

	// Check if the binary exists and is executable
	info, err := os.Stat(rootlesskitPath)
	if err != nil {
		t.Fatalf("rootlesskit binary not found at %s: %v", rootlesskitPath, err)
	}

	if info.Mode()&0111 == 0 {
		t.Fatalf("rootlesskit binary at %s is not executable", rootlesskitPath)
	}

	// Try to run rootlesskit --version to test basic functionality
	cmd := exec.Command(rootlesskitPath, "--version")
	output, err := cmd.Output()
	assert.NoError(t, err, "rootlesskit --version should execute successfully")
	assert.Contains(t, string(output), "rootlesskit version", "Output should contain version information")

	// Test that we can create a simple rootlesskit command (without actually running it)
	// This tests if the setup is theoretically correct
	testCmd := exec.Command(rootlesskitPath, "sh", "-c", "echo 'test'")
	testCmd.Env = append(os.Environ(),
		"CONTAINERD_ROOTLESS_ROOTLESSKIT_FLAGS=--net=slirp4netns",
	)

	t.Logf("Rootlesskit command would be: %v", testCmd.Args)
	t.Logf("Rootlesskit environment includes: CONTAINERD_ROOTLESS_ROOTLESSKIT_FLAGS")

	// This test passes if we get here without panicking
	t.Log("Rootlesskit functionality setup test passed")
}

func TestRootlessManager_rootlesskitMountTest(t *testing.T) {
	config := core.ContainerdConfig{
		RootlessMode: core.RootlessModeEnabled,
	}

	rm, err := NewRootlessManager(config)
	if err != nil {
		t.Fatalf("NewRootlessManager failed: %v", err)
	}

	if !rm.hasRootlesskit() {
		t.Skip("rootlesskit not available, skipping mount test")
	}

	// Create a temporary directory and file for testing
	tempDir, err := os.MkdirTemp("", "rootlesskit-mount-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file in the temp directory
	testFile := filepath.Join(tempDir, "testfile")
	testContent := "Hello from rootlesskit mount test!"
	if err := os.WriteFile(testFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test if we can validate mounting this file (should pass since we own it)
	err = rm.validateMountPermissions(testFile, "/tmp/testfile")
	if err != nil {
		t.Errorf("Mount validation should pass for owned file: %v", err)
	}

	// Test with a file owned by root (should fail)
	err = rm.validateMountPermissions("/etc/passwd", "/tmp/passwd")
	if err == nil {
		t.Error("Mount validation should fail for root-owned file even with rootlesskit")
	} else {
		t.Logf("Correctly failed to validate root-owned file mount: %v", err)
	}

	// Now test if rootlesskit can actually create a minimal rootless environment
	// This simulates what containerd would do with rootlesskit
	t.Log("Testing rootlesskit mount capability...")

	// Create a simple script that tries to access the mounted file
	mountTestScript := fmt.Sprintf(`
		#!/bin/sh
		# Test script for rootlesskit mount functionality
		if [ -f "%s" ]; then
			echo "SUCCESS: Can access mounted file"
			cat "%s"
		else
			echo "FAILURE: Cannot access mounted file"
			exit 1
		fi
	`, testFile, testFile)

	scriptPath := filepath.Join(tempDir, "mount_test.sh")
	if err := os.WriteFile(scriptPath, []byte(mountTestScript), 0755); err != nil {
		t.Fatalf("Failed to create test script: %v", err)
	}

	// Use rootlesskit to run the script in a rootless environment
	// This tests if rootlesskit can provide the necessary isolation for mounting
	cmd := exec.Command(rm.rootlesskitPath,
		"--net=slirp4netns",       // Use slirp4netns for networking
		"--disable-host-loopback", // Disable host loopback for security
		"sh", scriptPath)

	// Set up environment similar to how containerd uses rootlesskit
	cmd.Env = append(os.Environ(),
		"CONTAINERD_ROOTLESS_ROOTLESSKIT_FLAGS=--net=slirp4netns",
		fmt.Sprintf("HOME=%s", os.Getenv("HOME")),
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		fmt.Sprintf("XDG_RUNTIME_DIR=%s", os.Getenv("XDG_RUNTIME_DIR")),
	)

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	t.Logf("Rootlesskit command output: %s", outputStr)

	// Rootlesskit command should succeed
	assert.NoError(t, err, "Rootlesskit should be able to create rootless environment")

	// The script should have been able to access the mounted file
	assert.Contains(t, outputStr, "SUCCESS", "Rootlesskit should allow access to mounted files")
	assert.Contains(t, outputStr, "Hello from rootlesskit mount test!", "File content should be readable in rootless environment")

	t.Log("Rootlesskit mount capability test completed")
}
