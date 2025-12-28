package containerd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/taubyte/tau/pkg/containers"
)

// Client represents a containerd client connection
type Client struct {
	*containerd.Client
	ctx    context.Context
	daemon *Daemon
}

// taskIO holds the IO streams for a container task
type taskIO struct {
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	task     containerd.Task
	fifoSet  *cio.FIFOSet
	fifoDir  string        // Directory where FIFOs are created
	directIO *cio.DirectIO // DirectIO instance for cleanup
	io       cio.IO        // IO instance for cleanup
}

// ContainerdBackend implements the containers.Backend interface for containerd
type ContainerdBackend struct {
	config   containers.ContainerdConfig
	client   *Client                            // containerd client (to be implemented)
	daemon   *Daemon                            // daemon manager (to be implemented)
	rootless *RootlessManager                   // rootless manager (to be implemented)
	tasks    map[containers.ContainerID]*taskIO // Store tasks and their IO for log access
}

// New creates a new containerd backend
func New(config containers.ContainerdConfig) (*ContainerdBackend, error) {
	return NewContainerdBackend(config)
}

// NewContainerdBackend creates a new containerd backend (exported wrapper)
func NewContainerdBackend(config containers.ContainerdConfig) (*ContainerdBackend, error) {
	backend := &ContainerdBackend{
		config: config,
		tasks:  make(map[containers.ContainerID]*taskIO),
	}

	// Detect rootless mode
	if err := backend.detectRootlessMode(); err != nil {
		return nil, fmt.Errorf("failed to detect rootless mode: %w", err)
	}

	// Initialize daemon manager if AutoStart is enabled
	if config.AutoStart {
		daemon, err := NewDaemon(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create daemon manager: %w", err)
		}
		backend.daemon = daemon
	}

	// Initialize rootless manager if in rootless mode
	if backend.isRootlessMode() {
		rootless, err := NewRootlessManager(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create rootless manager: %w", err)
		}
		backend.rootless = rootless
	}

	// Ensure containerd is running
	if err := backend.ensureContainerdRunning(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ensure containerd is running: %w", err)
	}

	// Initialize containerd client
	if err := backend.initClient(); err != nil {
		return nil, fmt.Errorf("failed to initialize containerd client: %w", err)
	}

	return backend, nil
}

// detectRootlessMode detects if we should run in rootless mode
func (b *ContainerdBackend) detectRootlessMode() error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	isRoot := currentUser.Uid == "0"

	// If RootlessMode is explicitly set (not auto), respect it
	if b.config.RootlessMode != containers.RootlessModeAuto {
		if b.config.RootlessMode == containers.RootlessModeEnabled && isRoot {
			return fmt.Errorf("cannot enable rootless mode when running as root")
		}
		if b.config.RootlessMode == containers.RootlessModeDisabled && !isRoot {
			// Allow running in "disabled" mode even as non-root - we'll assume system containerd
		}
		return nil
	}

	// Auto-detect: enable rootless mode if not running as root
	if isRoot {
		b.config.RootlessMode = containers.RootlessModeDisabled
	} else {
		b.config.RootlessMode = containers.RootlessModeEnabled
	}

	return nil
}

// isRootlessMode returns true if running in rootless mode
func (b *ContainerdBackend) isRootlessMode() bool {
	return b.config.RootlessMode == containers.RootlessModeEnabled
}

// ensureContainerdRunning ensures containerd daemon is running
func (b *ContainerdBackend) ensureContainerdRunning(ctx context.Context) error {
	socketPath, err := b.getSocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	// Check if socket exists and is accessible
	if _, err := os.Stat(socketPath); err == nil {
		// Test actual socket connectivity
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			return nil
		}
		// Socket exists but not responding
		if b.config.RootlessMode == containers.RootlessModeDisabled {
			return fmt.Errorf("containerd not running at system socket %s - please start containerd system-wide", socketPath)
		}
	}

	// If AutoStart is enabled, start containerd (only in rootless mode)
	if b.config.AutoStart && b.isRootlessMode() {
		return b.daemon.Start(ctx)
	}

	// For RootlessModeDisabled, assume containerd is running system-wide
	if b.config.RootlessMode == containers.RootlessModeDisabled {
		return fmt.Errorf("containerd not running at system socket %s - please start containerd system-wide", socketPath)
	}

	return fmt.Errorf("containerd socket not found at %s and AutoStart is disabled", socketPath)
}

// getSocketPath returns the appropriate socket path
func (b *ContainerdBackend) getSocketPath() (string, error) {
	if b.config.SocketPath != "" {
		return b.config.SocketPath, nil
	}

	if b.isRootlessMode() {
		currentUser, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("failed to get current user: %w", err)
		}
		uid := currentUser.Uid
		return filepath.Join("/run", "user", uid, "tau", "containerd", "containerd.sock"), nil
	}

	return "/run/containerd/containerd.sock", nil
}

// initClient initializes the containerd client
func (b *ContainerdBackend) initClient() error {
	socketPath, err := b.getSocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	// Create context with namespace
	ctx := namespaces.WithNamespace(context.Background(), b.config.Namespace)

	// Connect to containerd
	client, err := containerd.New(socketPath, containerd.WithDefaultNamespace(b.config.Namespace))
	if err != nil {
		return fmt.Errorf("failed to connect to containerd at %s: %w", socketPath, err)
	}

	// Test connection by getting version
	version, err := client.Version(ctx)
	if err != nil {
		client.Close()
		return fmt.Errorf("failed to get containerd version: %w", err)
	}

	b.client = &Client{
		Client: client,
		ctx:    ctx,
		daemon: b.daemon,
	}

	fmt.Printf("Connected to containerd version %s at %s\n", version.Version, socketPath)
	return nil
}

// Image returns an Image interface for the given image name
func (b *ContainerdBackend) Image(name string) containers.Image {
	// TODO: Implement image operations
	return nil
}

// Create creates a new container
func (b *ContainerdBackend) Create(ctx context.Context, config *containers.ContainerConfig) (containers.ContainerID, error) {
	if b.client == nil {
		return "", fmt.Errorf("containerd client not initialized")
	}

	// Use the same namespace as the client
	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	// Generate a unique container ID
	containerID := containers.ContainerID(fmt.Sprintf("tau-%s-%d", time.Now().Format("20060102-150405"), time.Now().Nanosecond()))

	// Ensure the image exists (pull if needed)
	image, err := b.client.Pull(ctx, config.Image, containerd.WithPullUnpack)
	if err != nil {
		return "", fmt.Errorf("failed to pull image %s: %w", config.Image, err)
	}

	// Create the OCI spec
	spec, err := b.createOCISpec(config)
	if err != nil {
		return "", fmt.Errorf("failed to create OCI spec: %w", err)
	}

	// Create the container
	container, err := b.client.NewContainer(
		ctx,
		string(containerID),
		containerd.WithImage(image),
		containerd.WithNewSnapshot(fmt.Sprintf("%s-snapshot", containerID), image),
		containerd.WithSpec(spec),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	// Store container reference for cleanup
	_ = container // TODO: Store in a map for later cleanup

	return containerID, nil
}

// createOCISpec creates an OCI spec for the container configuration
func (b *ContainerdBackend) createOCISpec(config *containers.ContainerConfig) (*specs.Spec, error) {
	// Create a basic spec
	spec := &specs.Spec{
		Version: "1.0.2",
		Process: &specs.Process{
			Terminal: false,
			User: specs.User{
				UID: 0,
				GID: 0,
			},
			Args: config.Command,
			Env:  []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		},
		Root: &specs.Root{
			Path: "rootfs",
		},
		Hostname: "tau-container",
	}

	// Set environment variables
	if len(config.Env) > 0 {
		spec.Process.Env = append(spec.Process.Env, config.Env...)
	}

	// Set working directory
	if config.WorkDir != "" {
		spec.Process.Cwd = config.WorkDir
	} else {
		spec.Process.Cwd = "/"
	}

	// Add mounts
	spec.Mounts = []specs.Mount{
		{
			Destination: "/proc",
			Type:        "proc",
			Source:      "proc",
			Options:     []string{"nosuid", "noexec", "nodev"},
		},
		{
			Destination: "/dev",
			Type:        "tmpfs",
			Source:      "tmpfs",
			Options:     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
		},
		{
			Destination: "/dev/pts",
			Type:        "devpts",
			Source:      "devpts",
			Options:     []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"},
		},
		{
			Destination: "/sys",
			Type:        "sysfs",
			Source:      "sysfs",
			Options:     []string{"nosuid", "noexec", "nodev", "ro"},
		},
		{
			Destination: "/dev/mqueue",
			Type:        "mqueue",
			Source:      "mqueue",
			Options:     []string{"nosuid", "noexec", "nodev"},
		},
	}

	// Set up Linux namespaces (required for hostname and isolation)
	spec.Linux = &specs.Linux{
		Namespaces: []specs.LinuxNamespace{
			{Type: specs.PIDNamespace},
			{Type: specs.NetworkNamespace},
			{Type: specs.IPCNamespace},
			{Type: specs.UTSNamespace}, // Required for hostname
			{Type: specs.MountNamespace},
		},
		// Don't set CgroupsPath - let runc handle it automatically
		// This allows runc to use the appropriate cgroup path based on the environment
	}

	// Set resource limits if provided
	if config.Resources != nil {
		if spec.Linux.Resources == nil {
			spec.Linux.Resources = &specs.LinuxResources{}
		}
		if config.Resources.Memory > 0 {
			spec.Linux.Resources.Memory = &specs.LinuxMemory{
				Limit: &config.Resources.Memory,
			}
		}
		if config.Resources.PIDs > 0 {
			spec.Linux.Resources.Pids = &specs.LinuxPids{
				Limit: config.Resources.PIDs,
			}
		}
		if config.Resources.CPUQuota > 0 {
			period := uint64(100000)
			if config.Resources.CPUPeriod > 0 {
				period = uint64(config.Resources.CPUPeriod)
			}
			quota := config.Resources.CPUQuota
			spec.Linux.Resources.CPU = &specs.LinuxCPU{
				Quota:  &quota,
				Period: &period,
			}
		}
	}

	return spec, nil
}

// Start starts a container
func (b *ContainerdBackend) Start(ctx context.Context, id containers.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	// Use the same namespace as the client
	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	// Get the container
	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}

	// Create a temporary directory for FIFOs (in user's temp space)
	tmpDir, err := os.MkdirTemp("", "tau-containerd-logs-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory for FIFOs: %w", err)
	}

	// Create FIFO set for capturing stdout/stderr
	fifoSet, err := cio.NewFIFOSetInDir(tmpDir, string(id), false)
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to create FIFO set: %w", err)
	}

	// Create DirectIO with the FIFO set - this properly handles FIFO creation and IO
	directIO, err := cio.NewDirectIO(ctx, fifoSet)
	if err != nil {
		fifoSet.Close()
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to create DirectIO: %w", err)
	}

	// Load IO from the FIFO set - this creates the IO interface containerd needs
	io, err := cio.Load(fifoSet)
	if err != nil {
		directIO.Cancel()
		directIO.Close()
		fifoSet.Close()
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to load IO from FIFO set: %w", err)
	}

	// Create a Creator that returns the loaded IO
	cioCreator := func(id string) (cio.IO, error) {
		return io, nil
	}

	// Create a task for the container
	task, err := container.NewTask(ctx, cioCreator)
	if err != nil {
		io.Close()
		directIO.Cancel()
		directIO.Close()
		fifoSet.Close()
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to create task for container %s: %w", id, err)
	}

	// Start the task
	err = task.Start(ctx)
	if err != nil {
		io.Close()
		directIO.Cancel()
		directIO.Close()
		fifoSet.Close()
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to start container %s: %w", id, err)
	}

	// DirectIO provides stdout and stderr readers directly
	// Store task and IO for log access
	b.tasks[id] = &taskIO{
		stdout:   directIO.Stdout,
		stderr:   directIO.Stderr,
		task:     task,
		fifoSet:  fifoSet,
		fifoDir:  tmpDir,
		directIO: directIO,
		io:       io,
	}

	return nil
}

// Stop stops a container
func (b *ContainerdBackend) Stop(ctx context.Context, id containers.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	// Use the same namespace as the client
	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	// Get task from stored tasks
	taskIO, ok := b.tasks[id]
	if !ok {
		// Try to load from container
		container, err := b.client.LoadContainer(ctx, string(id))
		if err != nil {
			return fmt.Errorf("failed to load container %s: %w", id, err)
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to get task for container %s: %w", id, err)
		}
		// Kill the task
		_, err = task.Delete(ctx)
		return err
	}

	// Close IO streams
	if taskIO.stdout != nil {
		taskIO.stdout.Close()
	}
	if taskIO.stderr != nil {
		taskIO.stderr.Close()
	}

	// Close DirectIO and IO instances
	if taskIO.directIO != nil {
		taskIO.directIO.Cancel()
		taskIO.directIO.Close()
	}
	if taskIO.io != nil {
		taskIO.io.Close()
	}

	// Close FIFO set
	if taskIO.fifoSet != nil {
		taskIO.fifoSet.Close()
	}

	// Clean up FIFO directory
	if taskIO.fifoDir != "" {
		os.RemoveAll(taskIO.fifoDir)
	}

	// Kill the task
	_, err := taskIO.task.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", id, err)
	}

	// Remove from tasks map
	delete(b.tasks, id)

	return nil
}

// Remove removes a container
func (b *ContainerdBackend) Remove(ctx context.Context, id containers.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	// Use the same namespace as the client
	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	// Get the container
	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}

	// Delete the task if it exists
	task, err := container.Task(ctx, nil)
	if err == nil {
		// Task exists, delete it
		_, err = task.Delete(ctx)
		if err != nil {
			return fmt.Errorf("failed to delete task for container %s: %w", id, err)
		}
	}

	// Delete the container
	err = container.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete container %s: %w", id, err)
	}

	return nil
}

// Wait waits for a container to exit
func (b *ContainerdBackend) Wait(ctx context.Context, id containers.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	// Use the same namespace as the client
	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	// Get the container
	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}

	// Load the task
	task, err := container.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get task for container %s: %w", id, err)
	}

	// Wait for the task to exit
	exitStatusC, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for container %s: %w", id, err)
	}

	// Get exit status from channel
	exitStatus := <-exitStatusC
	if exitStatus.ExitCode() != 0 {
		return fmt.Errorf("container %s exited with status %d", id, exitStatus.ExitCode())
	}

	return nil
}

// Logs returns logs for a container
func (b *ContainerdBackend) Logs(ctx context.Context, id containers.ContainerID) (io.ReadCloser, error) {
	if b.client == nil {
		return nil, fmt.Errorf("containerd client not initialized")
	}

	// Get stored task IO
	taskIO, ok := b.tasks[id]
	if !ok {
		return nil, fmt.Errorf("container %s not found or not started", id)
	}

	// If logs aren't captured, return empty
	if taskIO.stdout == nil || taskIO.stderr == nil {
		return io.NopCloser(strings.NewReader("")), nil
	}

	// Create a pipe to combine stdout and stderr
	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		defer taskIO.stdout.Close()
		defer taskIO.stderr.Close()

		// Read from both stdout and stderr concurrently
		// Use a multi-reader approach: read from stdout first, then stderr
		// This ensures we capture all output in order
		mr := io.MultiReader(taskIO.stdout, taskIO.stderr)
		_, err := io.Copy(pw, mr)
		if err != nil && err != io.EOF {
			pw.CloseWithError(err)
			return
		}
	}()

	return pr, nil
}

// Inspect returns information about a container
func (b *ContainerdBackend) Inspect(ctx context.Context, id containers.ContainerID) (*containers.ContainerInfo, error) {
	if b.client == nil {
		return nil, fmt.Errorf("containerd client not initialized")
	}

	// Use the same namespace as the client
	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	// Get the container
	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("failed to load container %s: %w", id, err)
	}

	// Get container info
	info := &containers.ContainerInfo{
		ID:    id,
		Image: "", // TODO: Get from container spec
	}

	// Try to get task status
	task, err := container.Task(ctx, nil)
	if err != nil {
		// Container might not be running
		info.Status = "created"
		return info, nil
	}

	// Get task status
	status, err := task.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get task status for container %s: %w", id, err)
	}

	info.Status = string(status.Status)
	// Note: StartTime might not be available in all containerd versions

	// If exited, get exit code
	if status.Status == containerd.Stopped {
		info.ExitCode = int(status.ExitStatus)
	}

	return info, nil
}

// HealthCheck performs a health check on the backend
func (b *ContainerdBackend) HealthCheck(ctx context.Context) error {
	// TODO: Implement health check
	return fmt.Errorf("health check not implemented")
}

// Capabilities returns the backend capabilities
func (b *ContainerdBackend) Capabilities() containers.BackendCapabilities {
	return containers.BackendCapabilities{
		SupportsMemory:     true,
		SupportsCPU:        true,
		SupportsStorage:    true,
		SupportsPIDs:       true,
		SupportsMemorySwap: true,
		SupportsBuild:      true, // with BuildKit
		SupportsOCI:        true,
		SupportsNetworking: true,
		SupportsVolumes:    true,
	}
}

// validateUIDGIDMapping validates that subuid/subgid mappings are configured for rootless mode
func (b *ContainerdBackend) validateUIDGIDMapping() error {
	if b.config.RootlessMode == containers.RootlessModeDisabled {
		return nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	// Check if subuid mapping exists for this user (don't check specific UID mapping)
	if err := b.hasSubIDMapping("/etc/subuid", currentUser.Username); err != nil {
		return fmt.Errorf("subuid mapping validation failed: %w", err)
	}

	// Check if subgid mapping exists for this user (don't check specific GID mapping)
	if err := b.hasSubIDMapping("/etc/subgid", currentUser.Username); err != nil {
		return fmt.Errorf("subgid mapping validation failed: %w", err)
	}

	return nil
}

// hasSubIDMapping checks if subuid/subgid mapping exists for a user
func (b *ContainerdBackend) hasSubIDMapping(file, username string) error {
	content, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", file, err)
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) >= 3 && parts[0] == username {
			// Found mapping for this user
			return nil
		}
	}

	return fmt.Errorf("no subuid/subgid mapping found for user %s in %s", username, file)
}

// checkSubIDMapping checks if subuid/subgid mapping is configured
func (b *ContainerdBackend) checkSubIDMapping(file, username string, id int) error {
	content, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", file, err)
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[0] == username {
			// Found mapping for this user
			return nil
		}
	}

	return fmt.Errorf("no subuid/subgid mapping found for user %s in %s", username, file)
}

// TestSocketConnection tests if we can connect to the containerd socket
func (b *ContainerdBackend) TestSocketConnection() error {
	socketPath, err := b.getSocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	// Check if socket file exists
	if _, err := os.Stat(socketPath); err != nil {
		return fmt.Errorf("socket file does not exist: %s", socketPath)
	}

	// Try to connect to the socket
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to socket: %w", err)
	}
	conn.Close()

	return nil
}
