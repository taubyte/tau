//go:build linux

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
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/taubyte/tau/pkg/containers/core"
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

// ContainerdBackend implements the core.Backend interface for containerd
type ContainerdBackend struct {
	config     core.ContainerdConfig
	client     *Client                                   // containerd client (to be implemented)
	daemon     *Daemon                                   // daemon manager (to be implemented)
	rootless   *RootlessManager                          // rootless manager (to be implemented)
	tasks      map[core.ContainerID]*taskIO              // Store tasks and their IO for log access
	containers map[core.ContainerID]containerd.Container // Store containers for cleanup
}

// New creates a new containerd backend
func New(config core.ContainerdConfig) (*ContainerdBackend, error) {
	backend := &ContainerdBackend{
		config:     config,
		tasks:      make(map[core.ContainerID]*taskIO),
		containers: make(map[core.ContainerID]containerd.Container),
	}

	if err := backend.detectRootlessMode(); err != nil {
		return nil, fmt.Errorf("failed to detect rootless mode: %w", err)
	}

	// Only create daemon for rootless mode; in rootful mode containerd is managed by systemd
	if config.AutoStart && backend.isRootlessMode() {
		daemon, err := NewDaemon(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create daemon manager: %w", err)
		}
		backend.daemon = daemon
	}

	if backend.isRootlessMode() {
		rootless, err := NewRootlessManager(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create rootless manager: %w", err)
		}
		backend.rootless = rootless
	}

	if err := backend.ensureContainerdRunning(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ensure containerd is running: %w", err)
	}

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
	if b.config.RootlessMode != core.RootlessModeAuto {
		if b.config.RootlessMode == core.RootlessModeEnabled && isRoot {
			return fmt.Errorf("cannot enable rootless mode when running as root")
		}
		if b.config.RootlessMode == core.RootlessModeDisabled && !isRoot {
			// Allow running in "disabled" mode even as non-root - we'll assume system containerd
		}
		return nil
	}

	// Auto-detect: enable rootless mode if not running as root
	if isRoot {
		b.config.RootlessMode = core.RootlessModeDisabled
	} else {
		b.config.RootlessMode = core.RootlessModeEnabled
	}

	return nil
}

// isRootlessMode returns true if running in rootless mode
func (b *ContainerdBackend) isRootlessMode() bool {
	return b.config.RootlessMode == core.RootlessModeEnabled
}

// ensureContainerdRunning ensures containerd daemon is running
func (b *ContainerdBackend) ensureContainerdRunning(ctx context.Context) error {
	socketPath, err := b.getSocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	if _, err := os.Stat(socketPath); err == nil {
		if conn, err := net.Dial("unix", socketPath); err == nil {
			conn.Close()
			return nil
		}
		if b.config.RootlessMode == core.RootlessModeDisabled {
			return fmt.Errorf("containerd not running at system socket %s - please start containerd system-wide", socketPath)
		}
	}

	// If AutoStart is enabled, start containerd (only in rootless mode)
	if b.config.AutoStart && b.isRootlessMode() {
		return b.daemon.Start(ctx)
	}

	// For RootlessModeDisabled, assume containerd is running system-wide
	if b.config.RootlessMode == core.RootlessModeDisabled {
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

	ctx := namespaces.WithNamespace(context.Background(), b.config.Namespace)

	client, err := containerd.New(socketPath, containerd.WithDefaultNamespace(b.config.Namespace))
	if err != nil {
		return fmt.Errorf("failed to connect to containerd at %s: %w", socketPath, err)
	}

	if _, err := client.Version(ctx); err != nil {
		client.Close()
		return fmt.Errorf("failed to get containerd version: %w", err)
	}

	b.client = &Client{
		Client: client,
		ctx:    ctx,
		daemon: b.daemon,
	}

	return nil
}

// Image returns an Image interface for the given image name
func (b *ContainerdBackend) Image(name string) core.Image {
	return &containerdImage{
		backend: b,
		name:    name,
	}
}

// Create creates a new container
func (b *ContainerdBackend) Create(ctx context.Context, config *core.ContainerConfig) (core.ContainerID, error) {
	if b.client == nil {
		return "", fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	containerID := core.ContainerID(fmt.Sprintf("tau-%s-%d", time.Now().Format("20060102-150405"), time.Now().Nanosecond()))

	image, err := b.client.Pull(ctx, config.Image, containerd.WithPullUnpack)
	if err != nil {
		return "", fmt.Errorf("failed to pull image %s: %w", config.Image, err)
	}

	spec, err := b.createOCISpec(config)
	if err != nil {
		return "", fmt.Errorf("failed to create OCI spec: %w", err)
	}

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

	b.containers[containerID] = container

	return containerID, nil
}

// createOCISpec creates an OCI spec for the container configuration
func (b *ContainerdBackend) createOCISpec(config *core.ContainerConfig) (*specs.Spec, error) {
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

	if len(config.Env) > 0 {
		spec.Process.Env = append(spec.Process.Env, config.Env...)
	}

	if config.WorkDir != "" {
		spec.Process.Cwd = config.WorkDir
	} else {
		spec.Process.Cwd = "/"
	}

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

	spec.Linux = &specs.Linux{
		Namespaces: []specs.LinuxNamespace{
			{Type: specs.PIDNamespace},
			{Type: specs.NetworkNamespace},
			{Type: specs.IPCNamespace},
			{Type: specs.UTSNamespace}, // Required for hostname
			{Type: specs.MountNamespace},
		},
	}

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
func (b *ContainerdBackend) Start(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}

	tmpDir, err := os.MkdirTemp("", "tau-containerd-logs-*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory for FIFOs: %w", err)
	}

	fifoSet, err := cio.NewFIFOSetInDir(tmpDir, string(id), false)
	if err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to create FIFO set: %w", err)
	}

	directIO, err := cio.NewDirectIO(ctx, fifoSet)
	if err != nil {
		fifoSet.Close()
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to create DirectIO: %w", err)
	}

	io, err := cio.Load(fifoSet)
	if err != nil {
		directIO.Cancel()
		directIO.Close()
		fifoSet.Close()
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to load IO from FIFO set: %w", err)
	}

	cioCreator := func(id string) (cio.IO, error) {
		return io, nil
	}

	task, err := container.NewTask(ctx, cioCreator)
	if err != nil {
		io.Close()
		directIO.Cancel()
		directIO.Close()
		fifoSet.Close()
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to create task for container %s: %w", id, err)
	}

	err = task.Start(ctx)
	if err != nil {
		io.Close()
		directIO.Cancel()
		directIO.Close()
		fifoSet.Close()
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to start container %s: %w", id, err)
	}

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
func (b *ContainerdBackend) Stop(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	taskIO, ok := b.tasks[id]
	if !ok {
		container, err := b.client.LoadContainer(ctx, string(id))
		if err != nil {
			return fmt.Errorf("failed to load container %s: %w", id, err)
		}
		task, err := container.Task(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to get task for container %s: %w", id, err)
		}
		// Containerd requires the task to be stopped before deletion. If Status() fails, assume running.
		status, statusErr := task.Status(ctx)
		if statusErr != nil || status.Status == containerd.Running {
			if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
				if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
					return fmt.Errorf("failed to kill container %s: %w", id, err)
				}
			}
			exitStatusC, err := task.Wait(ctx)
			if err == nil {
				select {
				case <-exitStatusC:
				case <-time.After(5 * time.Second):
					task.Kill(ctx, syscall.SIGKILL)
					select {
					case <-exitStatusC:
					case <-time.After(3 * time.Second):
					}
				}
			}
		}
		_, err = task.Delete(ctx)
		return err
	}

	if taskIO.stdout != nil {
		taskIO.stdout.Close()
	}
	if taskIO.stderr != nil {
		taskIO.stderr.Close()
	}

	if taskIO.directIO != nil {
		taskIO.directIO.Cancel()
		taskIO.directIO.Close()
	}
	if taskIO.io != nil {
		taskIO.io.Close()
	}

	if taskIO.fifoSet != nil {
		taskIO.fifoSet.Close()
	}

	if taskIO.fifoDir != "" {
		os.RemoveAll(taskIO.fifoDir)
	}

	// Kill the task before deleting it (required for running tasks). If Status() fails, assume running.
	status, statusErr := taskIO.task.Status(ctx)
	if statusErr != nil || status.Status == containerd.Running {
		if err := taskIO.task.Kill(ctx, syscall.SIGTERM); err != nil {
			if err := taskIO.task.Kill(ctx, syscall.SIGKILL); err != nil {
				return fmt.Errorf("failed to kill container %s: %w", id, err)
			}
		}
		exitStatusC, err := taskIO.task.Wait(ctx)
		if err == nil {
			select {
			case <-exitStatusC:
			case <-time.After(5 * time.Second):
				taskIO.task.Kill(ctx, syscall.SIGKILL)
				// Containerd requires task to exit before Delete.
				select {
				case <-exitStatusC:
				case <-time.After(3 * time.Second):
				}
			}
		}
	}

	_, err := taskIO.task.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", id, err)
	}

	delete(b.tasks, id)

	return nil
}

// Remove removes a container
func (b *ContainerdBackend) Remove(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	var container containerd.Container
	var err error

	if storedContainer, exists := b.containers[id]; exists {
		container = storedContainer
	} else {
		container, err = b.client.LoadContainer(ctx, string(id))
		if err != nil {
			return fmt.Errorf("failed to load container %s: %w", id, err)
		}
	}

	task, err := container.Task(ctx, nil)
	if err == nil {
		_, err = task.Delete(ctx)
		if err != nil {
			return fmt.Errorf("failed to delete task for container %s: %w", id, err)
		}
	}

	err = container.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete container %s: %w", id, err)
	}

	delete(b.containers, id)

	return nil
}

// Wait waits for a container to exit
func (b *ContainerdBackend) Wait(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get task for container %s: %w", id, err)
	}

	exitStatusC, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for container %s: %w", id, err)
	}

	exitStatus := <-exitStatusC
	if exitStatus.ExitCode() != 0 {
		return fmt.Errorf("container %s exited with status %d", id, exitStatus.ExitCode())
	}

	return nil
}

// Logs returns logs for a container
func (b *ContainerdBackend) Logs(ctx context.Context, id core.ContainerID) (io.ReadCloser, error) {
	if b.client == nil {
		return nil, fmt.Errorf("containerd client not initialized")
	}

	taskIO, ok := b.tasks[id]
	if !ok {
		return nil, fmt.Errorf("container %s not found or not started", id)
	}

	if taskIO.stdout == nil || taskIO.stderr == nil {
		return io.NopCloser(strings.NewReader("")), nil
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		defer taskIO.stdout.Close()
		defer taskIO.stderr.Close()

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
func (b *ContainerdBackend) Inspect(ctx context.Context, id core.ContainerID) (*core.ContainerInfo, error) {
	if b.client == nil {
		return nil, fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("failed to load container %s: %w", id, err)
	}

	info := &core.ContainerInfo{
		ID:    id,
		Image: "", // TODO: Get from container spec
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		info.Status = "created"
		return info, nil
	}

	status, err := task.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get task status for container %s: %w", id, err)
	}

	info.Status = string(status.Status)

	if status.Status == containerd.Stopped {
		info.ExitCode = int(status.ExitStatus)
	}

	return info, nil
}

// HealthCheck performs a health check on the backend
func (b *ContainerdBackend) HealthCheck(ctx context.Context) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	_, err := b.client.Version(ctx)
	if err != nil {
		return fmt.Errorf("containerd daemon not responding: %w", err)
	}

	return nil
}

// Capabilities returns the backend capabilities
func (b *ContainerdBackend) Capabilities() core.BackendCapabilities {
	return core.BackendCapabilities{
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
	if b.config.RootlessMode == core.RootlessModeDisabled {
		return nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	if err := b.hasSubIDMapping("/etc/subuid", currentUser.Username); err != nil {
		return fmt.Errorf("subuid mapping validation failed: %w", err)
	}

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
			return nil
		}
	}

	return fmt.Errorf("no subuid/subgid mapping found for user %s in %s", username, file)
}

// testSocketConnection checks if we can connect to the containerd socket.
func (b *ContainerdBackend) testSocketConnection() error {
	socketPath, err := b.getSocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	if _, err := os.Stat(socketPath); err != nil {
		return fmt.Errorf("socket file does not exist: %s", socketPath)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("failed to connect to socket: %w", err)
	}
	conn.Close()

	return nil
}
