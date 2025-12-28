package containerd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/taubyte/tau/pkg/containers"
)

// Daemon manages containerd daemon lifecycle for auto-start functionality
type Daemon struct {
	config     containers.ContainerdConfig
	process    *os.Process
	socketPath string
	stateFile  string
}

// NewDaemon creates a new daemon manager
func NewDaemon(config containers.ContainerdConfig) (*Daemon, error) {
	// Get current user UID
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}

	uid, err := strconv.Atoi(currentUser.Uid)
	if err != nil {
		return nil, fmt.Errorf("failed to parse user UID: %w", err)
	}

	// Use XDG_RUNTIME_DIR for socket and state (like Docker)
	xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntimeDir == "" {
		xdgRuntimeDir = filepath.Join("/run", "user", strconv.Itoa(uid))
	}
	containerdDir := filepath.Join(xdgRuntimeDir, "tau", "containerd")

	// Create socket directory
	if err := os.MkdirAll(containerdDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", containerdDir, err)
	}

	socketPath := filepath.Join(containerdDir, "containerd.sock")
	stateFile := filepath.Join(containerdDir, "containerd.pid")

	return &Daemon{
		config:     config,
		socketPath: socketPath,
		stateFile:  stateFile,
	}, nil
}

// Start starts the containerd daemon if not already running
func (d *Daemon) Start(ctx context.Context) error {
	// Check if already running
	if d.isRunning() {
		return nil
	}

	// Find containerd binary
	containerdPath, err := d.findContainerdBinary()
	if err != nil {
		return fmt.Errorf("failed to find containerd binary: %w", err)
	}

	// Determine if we need rootless mode
	isRootless := d.config.RootlessMode == containers.RootlessModeEnabled ||
		(d.config.RootlessMode == containers.RootlessModeAuto && os.Geteuid() != 0)

	if isRootless {
		return d.startWithRootlesskit(ctx, containerdPath)
	}

	// Root mode - start containerd directly (not implemented for now)
	return fmt.Errorf("root mode containerd startup not yet implemented")
}

// startWithRootlesskit starts containerd via rootlesskit (like nerdctl does)
func (d *Daemon) startWithRootlesskit(ctx context.Context, containerdPath string) error {
	// Find rootlesskit binary
	rootlesskitPath, err := d.findRootlesskitBinary()
	if err != nil {
		return fmt.Errorf("rootlesskit is required for rootless mode: %w", err)
	}

	// Detect network driver
	netDriver, err := d.detectRootlesskitNetwork()
	if err != nil {
		return fmt.Errorf("failed to detect rootlesskit network driver: %w", err)
	}

	// Get XDG directories
	xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntimeDir == "" {
		currentUser, err := user.Current()
		if err != nil {
			return fmt.Errorf("failed to get current user: %w", err)
		}
		uid, err := strconv.Atoi(currentUser.Uid)
		if err != nil {
			return fmt.Errorf("failed to parse user UID: %w", err)
		}
		xdgRuntimeDir = filepath.Join("/run", "user", strconv.Itoa(uid))
	}

	xdgDataHome := os.Getenv("XDG_DATA_HOME")
	if xdgDataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		xdgDataHome = filepath.Join(home, ".local", "share")
	}

	xdgConfigHome := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfigHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		xdgConfigHome = filepath.Join(home, ".config")
	}

	// Set up directories (matching Docker's structure)
	rootDir := filepath.Join(xdgDataHome, "tau", "containerd", "daemon")
	stateDir := filepath.Join(xdgRuntimeDir, "tau", "containerd", "daemon")
	rootlesskitStateDir := filepath.Join(xdgRuntimeDir, "tau-containerd-rootless")

	// Create directories
	for _, dir := range []string{rootDir, stateDir, rootlesskitStateDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Inside rootlesskit namespace, socket will be at /run/containerd/containerd.sock
	// (after bind-mounting /run/containerd to xdgRuntimeDir/tau/containerd)
	socketPathInNamespace := "/run/containerd/containerd.sock"
	debugSocketPathInNamespace := "/run/containerd/containerd-debug.sock"

	// Config file goes in the host directory (same as socket directory on host)
	configDir := filepath.Dir(d.socketPath)

	// Create config file with namespace paths
	configPath, err := d.createConfigFile(rootDir, stateDir, socketPathInNamespace, debugSocketPathInNamespace, configDir)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}

	// Create wrapper script that does bind-mounts and execs containerd
	// The socket directory needs to be created before bind-mounting
	socketDir := filepath.Dir(d.socketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	wrapperScript, err := d.createRootlesskitWrapper(containerdPath, configPath, rootDir, stateDir, socketDir, xdgRuntimeDir, xdgDataHome, xdgConfigHome)
	if err != nil {
		return fmt.Errorf("failed to create rootlesskit wrapper: %w", err)
	}
	defer os.Remove(wrapperScript) // Clean up script after use

	// Determine MTU based on network driver
	mtu := "65520"
	if netDriver != "slirp4netns" && netDriver != "pasta" {
		mtu = "1500"
	}

	// Build rootlesskit command (like nerdctl does)
	cmd := exec.CommandContext(ctx, rootlesskitPath,
		"--state-dir", rootlesskitStateDir,
		"--net", netDriver,
		"--mtu", mtu,
		"--slirp4netns-sandbox=auto",
		"--slirp4netns-seccomp=auto",
		"--disable-host-loopback",
		"--port-driver=builtin",
		"--copy-up=/etc",
		"--copy-up=/run",
		"--copy-up=/var/lib",
		"--propagation=rslave",
		"/bin/sh", wrapperScript,
	)

	// Set environment
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"XDG_RUNTIME_DIR="+xdgRuntimeDir,
		"XDG_DATA_HOME="+xdgDataHome,
		"XDG_CONFIG_HOME="+xdgConfigHome,
	)

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start containerd via rootlesskit: %w", err)
	}

	d.process = cmd.Process

	// Save PID to state file
	if err := os.WriteFile(d.stateFile, []byte(fmt.Sprintf("%d", d.process.Pid)), 0644); err != nil {
		d.process.Kill()
		return fmt.Errorf("failed to save PID: %w", err)
	}

	// Wait for socket to be available
	if err := d.waitForSocket(ctx, 30*time.Second); err != nil {
		d.process.Kill()
		os.Remove(d.stateFile)
		return fmt.Errorf("containerd failed to start via rootlesskit: %w", err)
	}

	return nil
}

// createRootlesskitWrapper creates a shell script that does bind-mounts and execs containerd
// This script runs inside the rootlesskit child namespace
func (d *Daemon) createRootlesskitWrapper(containerdPath, configPath, rootDir, stateDir, socketDir, xdgRuntimeDir, xdgDataHome, xdgConfigHome string) (string, error) {
	// Create temporary script file
	tmpFile, err := os.CreateTemp("", "tau-containerd-rootless-*.sh")
	if err != nil {
		return "", fmt.Errorf("failed to create temp script: %w", err)
	}
	scriptPath := tmpFile.Name()
	tmpFile.Close()

	// Inside rootlesskit, /run/containerd will be bind-mounted to socketDir
	// So the socket will be at /run/containerd/containerd.sock
	socketDirInsideNamespace := socketDir

	// Write script content (based on nerdctl's containerd-rootless.sh)
	script := fmt.Sprintf(`#!/bin/sh
set -e

# Remove symlinks in parent namespace (they become symlinks in child namespace)
rm -f /run/containerd /run/xtables.lock /var/lib/containerd /var/lib/cni /etc/containerd

# Bind-mount /etc/ssl (workaround for certificate issues)
if [ -L "/etc/ssl" ]; then
	realpath_etc_ssl=$(realpath /etc/ssl)
	rm -f /etc/ssl
	mkdir /etc/ssl
	mount --rbind "${realpath_etc_ssl}" /etc/ssl
fi

# Bind-mount /run/containerd
mkdir -p %q /run/containerd
mount --bind %q /run/containerd

# Bind-mount /var/lib/containerd
mkdir -p %q /var/lib/containerd
mount --bind %q /var/lib/containerd

# Bind-mount /var/lib/cni
mkdir -p %q /var/lib/cni
mount --bind %q /var/lib/cni

# Bind-mount /etc/containerd
mkdir -p %q /etc/containerd
mount --bind %q /etc/containerd

# Exec containerd (it will read config which has the correct socket path)
exec %q --config %q
`,
		socketDirInsideNamespace,
		socketDirInsideNamespace,
		rootDir,
		rootDir,
		filepath.Join(xdgDataHome, "tau", "cni"),
		filepath.Join(xdgDataHome, "tau", "cni"),
		filepath.Join(xdgConfigHome, "tau", "containerd"),
		filepath.Join(xdgConfigHome, "tau", "containerd"),
		containerdPath,
		configPath,
	)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		os.Remove(scriptPath)
		return "", fmt.Errorf("failed to write wrapper script: %w", err)
	}

	return scriptPath, nil
}

// Stop stops the containerd daemon
func (d *Daemon) Stop(ctx context.Context) error {
	if !d.isRunning() {
		return nil
	}

	// If we have a process, kill it
	if d.process != nil {
		// Kill the process
		if err := d.process.Kill(); err != nil {
			// Process might already be dead, try to wait anyway
			d.process.Wait()
		} else {
			// Wait for process to exit
			_, err := d.process.Wait()
			if err != nil && err.Error() != "signal: killed" {
				// Ignore "signal: killed" error as it's expected
			}
		}
		d.process = nil
	} else {
		// Try to find and kill process from PID file
		if data, err := os.ReadFile(d.stateFile); err == nil {
			var pid int
			if n, err := fmt.Sscanf(string(data), "%d", &pid); err == nil && n == 1 {
				if process, err := os.FindProcess(pid); err == nil {
					process.Kill()
					process.Wait()
				}
			}
		}
	}

	// Clean up state file and socket
	os.Remove(d.stateFile)
	os.Remove(d.socketPath)

	return nil
}

// isRunning checks if the containerd daemon is running
func (d *Daemon) isRunning() bool {
	if d.process == nil {
		// Check if state file exists and contains a valid PID
		if data, err := os.ReadFile(d.stateFile); err == nil {
			var pid int
			if n, err := fmt.Sscanf(string(data), "%d", &pid); err == nil && n == 1 {
				// Check if process is still running
				process, err := os.FindProcess(pid)
				if err == nil {
					// Send signal 0 to check if process exists
					if err := process.Signal(syscall.Signal(0)); err == nil {
						d.process = process
						return true
					}
				}
			}
		}
		// Check if socket exists and is actually responding
		if d.isSocketReady() {
			return true
		}
		return false
	}

	// Check if process is still alive
	return d.process.Signal(syscall.Signal(0)) == nil
}

// findContainerdBinary finds the containerd binary path
func (d *Daemon) findContainerdBinary() (string, error) {
	if d.config.ContainerdPath != "" {
		return d.config.ContainerdPath, nil
	}

	// Check PATH
	if path, err := exec.LookPath("containerd"); err == nil {
		return path, nil
	}

	// TODO: Implement auto-download logic
	return "", fmt.Errorf("containerd binary not found in PATH")
}

// findRootlesskitBinary finds the rootlesskit binary path
func (d *Daemon) findRootlesskitBinary() (string, error) {
	if d.config.RootlesskitPath != "" {
		return d.config.RootlesskitPath, nil
	}

	// Check common locations
	for _, name := range []string{"rootlesskit", "docker-rootlesskit"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("rootlesskit binary not found in PATH")
}

// detectRootlesskitNetwork detects the best network driver for rootlesskit
func (d *Daemon) detectRootlesskitNetwork() (string, error) {
	// Check for slirp4netns (preferred)
	if path, err := exec.LookPath("slirp4netns"); err == nil {
		// Check if it supports --netns-type (>= v0.4.0)
		cmd := exec.Command(path, "--help")
		output, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(output), "--netns-type") {
			return "slirp4netns", nil
		}
	}

	// Check for pasta
	if _, err := exec.LookPath("pasta"); err == nil {
		return "pasta", nil
	}

	// Check for vpnkit
	if _, err := exec.LookPath("vpnkit"); err == nil {
		return "vpnkit", nil
	}

	return "", fmt.Errorf("no suitable rootlesskit network driver found (need slirp4netns >= v0.4.0, pasta, or vpnkit)")
}

// createConfigFile creates a containerd config file for rootless mode
// Matches Docker's minimal config format
// socketPath and debugSocketPath are paths inside the rootlesskit namespace
// configDir is the directory on the host where the config file should be created
func (d *Daemon) createConfigFile(rootDir, stateDir, socketPath, debugSocketPath, configDir string) (string, error) {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create containerd directory: %w", err)
	}

	configPath := filepath.Join(configDir, "containerd.toml")

	// Build minimal config matching Docker's format
	// Docker disables CRI plugin and uses minimal settings
	config := fmt.Sprintf(`disabled_plugins = ["io.containerd.grpc.v1.cri"]
imports = []
oom_score = 0
required_plugins = []
root = %q
state = %q
temp = ""
version = 2

[cgroup]
  path = ""

[debug]
  address = %q
  format = "text"
  gid = 0
  level = ""
  uid = 0

[grpc]
  address = %q
  gid = 0
  max_recv_message_size = 16777216
  max_send_message_size = 16777216
  tcp_address = ""
  tcp_tls_ca = ""
  tcp_tls_cert = ""
  tcp_tls_common_name = ""
  tcp_tls_key = ""
  uid = 0

[metrics]
  address = ""
  grpc_histogram = false

[plugins]

[proxy_plugins]

[stream_processors]

[timeouts]

[ttrpc]
  address = ""
  gid = 0
  uid = 0
`, rootDir, stateDir, debugSocketPath, socketPath)

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		return "", fmt.Errorf("failed to write config file: %w", err)
	}

	return configPath, nil
}

// waitForSocket waits for the containerd socket to be available
func (d *Daemon) waitForSocket(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if d.isSocketReady() {
				return nil
			}
		}
	}
}

// isSocketReady checks if the socket is ready for connections
func (d *Daemon) isSocketReady() bool {
	// First check if socket file exists
	if _, err := os.Stat(d.socketPath); err != nil {
		return false
	}

	// Try to connect to the socket to test if containerd is responding
	conn, err := d.connectToSocket()
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// connectToSocket attempts to connect to the containerd socket
func (d *Daemon) connectToSocket() (net.Conn, error) {
	return net.Dial("unix", d.socketPath)
}

// HealthCheck checks if the daemon is healthy
func (d *Daemon) HealthCheck(ctx context.Context) error {
	if !d.isRunning() {
		return fmt.Errorf("containerd daemon is not running")
	}

	// TODO: Implement more thorough health check
	return nil
}
