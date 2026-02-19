//go:build linux

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

	"github.com/taubyte/tau/pkg/containers/core"
)

// Daemon manages containerd daemon lifecycle for auto-start functionality
type Daemon struct {
	config     core.ContainerdConfig
	process    *os.Process
	socketPath string
	stateFile  string
}

// NewDaemon creates a new daemon manager
func NewDaemon(config core.ContainerdConfig) (*Daemon, error) {
	// Determine if we need rootless mode
	isRootless := config.RootlessMode == core.RootlessModeEnabled ||
		(config.RootlessMode == core.RootlessModeAuto && os.Geteuid() != 0)

	var socketPath, stateFile string

	if isRootless {
		// Rootless mode: use XDG directories
		currentUser, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("failed to get current user: %w", err)
		}

		uid, err := strconv.Atoi(currentUser.Uid)
		if err != nil {
			return nil, fmt.Errorf("failed to parse user UID: %w", err)
		}

		xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if xdgRuntimeDir == "" {
			xdgRuntimeDir = filepath.Join("/run", "user", strconv.Itoa(uid))
		}
		containerdDir := filepath.Join(xdgRuntimeDir, "tau", "containerd")

		if err := os.MkdirAll(containerdDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", containerdDir, err)
		}

		socketPath = filepath.Join(containerdDir, "containerd.sock")
		stateFile = filepath.Join(containerdDir, "containerd.pid")
	} else {
		// Rootful mode: use standard system paths or explicit SocketPath (e.g. for testing)
		if config.SocketPath != "" {
			socketPath = config.SocketPath
			stateFile = filepath.Join(filepath.Dir(socketPath), "containerd.pid")
		} else {
			socketPath = "/run/containerd/containerd.sock"
			stateFile = "/run/containerd/containerd.pid"

			socketDir := filepath.Dir(socketPath)
			if err := os.MkdirAll(socketDir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create directory %s: %w", socketDir, err)
			}
		}
	}

	return &Daemon{
		config:     config,
		socketPath: socketPath,
		stateFile:  stateFile,
	}, nil
}

// Start starts the containerd daemon if not already running
func (d *Daemon) Start(ctx context.Context) error {
	if d.isRunning() {
		return nil
	}

	isRootless := d.config.RootlessMode == core.RootlessModeEnabled ||
		(d.config.RootlessMode == core.RootlessModeAuto && os.Geteuid() != 0)

	if !isRootless {
		return fmt.Errorf("rootful mode: containerd is managed by systemd, please start it via systemd")
	}

	containerdPath, err := d.findContainerdBinary()
	if err != nil {
		return fmt.Errorf("failed to find containerd binary: %w", err)
	}

	return d.startWithRootlesskit(ctx, containerdPath)
}

// startWithRootlesskit starts containerd via rootlesskit (like nerdctl does)
func (d *Daemon) startWithRootlesskit(ctx context.Context, containerdPath string) error {
	rootlesskitPath, err := d.findRootlesskitBinary()
	if err != nil {
		return fmt.Errorf("rootlesskit is required for rootless mode: %w", err)
	}

	netDriver, err := d.detectRootlesskitNetwork()
	if err != nil {
		return fmt.Errorf("failed to detect rootlesskit network driver: %w", err)
	}

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

	rootDir := filepath.Join(xdgDataHome, "tau", "containerd", "daemon")
	stateDir := filepath.Join(xdgRuntimeDir, "tau", "containerd", "daemon")
	rootlesskitStateDir := filepath.Join(xdgRuntimeDir, "tau-containerd-rootless")

	for _, dir := range []string{rootDir, stateDir, rootlesskitStateDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	socketPathInNamespace := "/run/containerd/containerd.sock"
	debugSocketPathInNamespace := "/run/containerd/containerd-debug.sock"
	configDir := filepath.Dir(d.socketPath)

	configPath, err := d.createConfigFile(rootDir, stateDir, socketPathInNamespace, debugSocketPathInNamespace, configDir)
	if err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}

	socketDir := filepath.Dir(d.socketPath)
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return fmt.Errorf("failed to create socket directory: %w", err)
	}

	wrapperScript, err := d.createRootlesskitWrapper(containerdPath, configPath, rootDir, stateDir, socketDir, xdgRuntimeDir, xdgDataHome, xdgConfigHome)
	if err != nil {
		return fmt.Errorf("failed to create rootlesskit wrapper: %w", err)
	}
	defer os.Remove(wrapperScript)

	mtu := "65520"
	if netDriver != "slirp4netns" && netDriver != "pasta" {
		mtu = "1500"
	}

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

	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"XDG_RUNTIME_DIR="+xdgRuntimeDir,
		"XDG_DATA_HOME="+xdgDataHome,
		"XDG_CONFIG_HOME="+xdgConfigHome,
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start containerd via rootlesskit: %w", err)
	}

	d.process = cmd.Process

	if err := os.WriteFile(d.stateFile, []byte(fmt.Sprintf("%d", d.process.Pid)), 0644); err != nil {
		d.process.Kill()
		return fmt.Errorf("failed to save PID: %w", err)
	}

	if err := d.waitForSocket(ctx, 30*time.Second); err != nil {
		d.process.Kill()
		os.Remove(d.stateFile)
		return fmt.Errorf("containerd failed to start via rootlesskit: %w", err)
	}

	return nil
}

// createRootlesskitWrapper creates a shell script that does bind-mounts and execs containerd
func (d *Daemon) createRootlesskitWrapper(containerdPath, configPath, rootDir, stateDir, socketDir, xdgRuntimeDir, xdgDataHome, xdgConfigHome string) (string, error) {
	tmpFile, err := os.CreateTemp("", "tau-containerd-rootless-*.sh")
	if err != nil {
		return "", fmt.Errorf("failed to create temp script: %w", err)
	}
	scriptPath := tmpFile.Name()
	tmpFile.Close()

	socketDirInsideNamespace := socketDir

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

	if d.process != nil {
		if err := d.process.Kill(); err != nil {
			// Process might already be dead, try to wait anyway
			d.process.Wait()
		} else {
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

	os.Remove(d.stateFile)
	os.Remove(d.socketPath)

	return nil
}

// isRunning checks if the containerd daemon is running
func (d *Daemon) isRunning() bool {
	if d.process == nil {
		if data, err := os.ReadFile(d.stateFile); err == nil {
			var pid int
			if n, err := fmt.Sscanf(string(data), "%d", &pid); err == nil && n == 1 {
				process, err := os.FindProcess(pid)
				if err == nil {
					if err := process.Signal(syscall.Signal(0)); err == nil {
						d.process = process
						return true
					}
				}
			}
		}
		if d.isSocketReady() {
			return true
		}
		return false
	}

	return d.process.Signal(syscall.Signal(0)) == nil
}

// findContainerdBinary finds the containerd binary path
func (d *Daemon) findContainerdBinary() (string, error) {
	if d.config.ContainerdPath != "" {
		return d.config.ContainerdPath, nil
	}

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

	for _, name := range []string{"rootlesskit", "docker-rootlesskit"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("rootlesskit binary not found in PATH")
}

// detectRootlesskitNetwork detects the best network driver for rootlesskit
func (d *Daemon) detectRootlesskitNetwork() (string, error) {
	if path, err := exec.LookPath("slirp4netns"); err == nil {
		cmd := exec.Command(path, "--help")
		output, err := cmd.CombinedOutput()
		if err == nil && strings.Contains(string(output), "--netns-type") {
			return "slirp4netns", nil
		}
	}

	if _, err := exec.LookPath("pasta"); err == nil {
		return "pasta", nil
	}

	// Check for vpnkit
	if _, err := exec.LookPath("vpnkit"); err == nil {
		return "vpnkit", nil
	}

	return "", fmt.Errorf("no suitable rootlesskit network driver found (need slirp4netns >= v0.4.0, pasta, or vpnkit)")
}

// createConfigFile creates a containerd config file
func (d *Daemon) createConfigFile(rootDir, stateDir, socketPath, debugSocketPath, configDir string) (string, error) {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create containerd directory: %w", err)
	}

	configPath := filepath.Join(configDir, "containerd.toml")

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
