//go:build linux

package containerd

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/errdefs"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/taubyte/tau/pkg/containers/core"
)

// Client represents a containerd client connection
type Client struct {
	*containerd.Client
	ctx    context.Context
	daemon *Daemon
}

// taskIO holds the IO streams for a container task, and the output collected
// from them.
type taskIO struct {
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	task     containerd.Task
	fifoSet  *cio.FIFOSet
	fifoDir  string        // Directory where FIFOs are created
	directIO *cio.DirectIO // DirectIO instance for cleanup
	io       cio.IO        // IO instance for cleanup

	// logs holds the container's output, docker-framed, collected as it is
	// produced. drained closes once both streams have hit EOF.
	//
	// ponytail: whole output in memory; spill to fifoDir if a workload ever
	// outgrows that.
	logsMu  sync.Mutex
	logs    []byte
	drained sync.WaitGroup
}

// drain starts copying the task's output the moment it starts running.
//
// containerd keeps no log store: these FIFOs are the only sink, and a container
// that fills their pipe buffer — roughly 64KiB — blocks on write until someone
// reads. Collecting only when Logs is called therefore wedges any container
// that says more than that, which is every real build.
func (t *taskIO) drain() {
	for _, s := range []struct {
		reader io.Reader
		stream byte
	}{
		{t.stdout, streamStdout},
		{t.stderr, streamStderr},
	} {
		if s.reader == nil {
			continue
		}
		t.drained.Add(1)
		go func(reader io.Reader, stream byte) {
			defer t.drained.Done()
			// A read error ends this stream; whatever arrived is kept, since
			// partial build output is still worth showing.
			io.Copy(&streamFramer{mu: &t.logsMu, w: (*logSink)(t), stream: stream}, reader)
		}(s.reader, s.stream)
	}
}

// logSink appends to a taskIO's collected output. The framer already holds
// logsMu when it writes.
type logSink taskIO

func (s *logSink) Write(p []byte) (int, error) {
	s.logs = append(s.logs, p...)
	return len(p), nil
}

// collected returns the output gathered so far, once both streams have ended.
func (t *taskIO) collected(ctx context.Context) ([]byte, error) {
	done := make(chan struct{})
	go func() {
		t.drained.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	t.logsMu.Lock()
	defer t.logsMu.Unlock()

	return slices.Clone(t.logs), nil
}

// ContainerdBackend implements the core.Backend interface for containerd
type ContainerdBackend struct {
	config core.ContainerdConfig
	client *Client // containerd client (to be implemented)
	daemon *Daemon // daemon manager (to be implemented)

	// mu guards tasks and containers: a backend is shared across goroutines
	// (one per build step), and Create/Start/Stop/Remove all mutate both.
	mu         sync.Mutex
	tasks      map[core.ContainerID]*taskIO              // Store tasks and their IO for log access
	containers map[core.ContainerID]containerd.Container // Store containers for cleanup
}

func (b *ContainerdBackend) putTask(id core.ContainerID, t *taskIO) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tasks[id] = t
}

func (b *ContainerdBackend) takeTask(id core.ContainerID) (*taskIO, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.tasks[id]
	if ok {
		delete(b.tasks, id)
	}
	return t, ok
}

func (b *ContainerdBackend) getTask(id core.ContainerID) (*taskIO, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.tasks[id]
	return t, ok
}

func (b *ContainerdBackend) putContainer(id core.ContainerID, c containerd.Container) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.containers[id] = c
}

func (b *ContainerdBackend) takeContainer(id core.ContainerID) (containerd.Container, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.containers[id]
	if ok {
		delete(b.containers, id)
	}
	return c, ok
}

// close releases the task's IO: the FIFO readers, the DirectIO/cio pair and the
// temp directory holding the FIFOs. Safe to call more than once.
func (t *taskIO) close() {
	if t.stdout != nil {
		t.stdout.Close()
	}
	if t.stderr != nil {
		t.stderr.Close()
	}
	if t.directIO != nil {
		t.directIO.Cancel()
		t.directIO.Close()
	}
	if t.io != nil {
		t.io.Close()
	}
	if t.fifoSet != nil {
		t.fifoSet.Close()
	}
	if t.fifoDir != "" {
		os.RemoveAll(t.fifoDir)
		t.fifoDir = ""
	}
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
// BackendType returns core.BackendTypeContainerd
func (b *ContainerdBackend) BackendType() core.BackendType {
	return core.BackendTypeContainerd
}

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

	image, err := b.client.GetImage(ctx, config.Image)
	if err != nil {
		if image, err = b.client.Pull(ctx, config.Image, containerd.WithPullUnpack); err != nil {
			return "", fmt.Errorf("failed to pull image %s: %w", config.Image, err)
		}
	}

	opts, err := specOpts(config)
	if err != nil {
		return "", fmt.Errorf("failed to build OCI spec options: %w", err)
	}

	if b.isRootlessMode() {
		if config.Resources != nil {
			return "", fmt.Errorf("resource limits require rootful containerd: a rootless daemon cannot create the cgroup to enforce them")
		}
		opts = append(opts, withoutCgroups())
	}

	// The image's own configuration is the baseline, exactly as docker treats
	// it, and tau's options are layered on top.
	imageOpts, err := imageSpecOpts(ctx, image)
	if err != nil {
		return "", fmt.Errorf("failed to read config of image %s: %w", config.Image, err)
	}

	container, err := b.client.NewContainer(
		ctx,
		string(containerID),
		containerd.WithImage(image),
		containerd.WithNewSnapshot(fmt.Sprintf("%s-snapshot", containerID), image),
		containerd.WithNewSpec(append(imageOpts, opts...)...),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	b.putContainer(containerID, container)

	return containerID, nil
}

// imageSpecOpts carries the image's own environment, working directory and
// entrypoint into the spec, so that running an image on containerd means what
// it means on docker.
//
// The config blob is read directly rather than through oci.WithImageConfig,
// which mounts the image's snapshot to resolve user names. Rootless containerd
// is not permitted to make that mount, and rootless is the mode this backend
// exists to support.
//
// The image's USER is therefore not applied: resolving a name to a uid needs
// that same mount. Containers run as root, which is what build images expect.
func imageSpecOpts(ctx context.Context, image containerd.Image) ([]oci.SpecOpts, error) {
	descriptor, err := image.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get image config descriptor: %w", err)
	}

	blob, err := content.ReadBlob(ctx, image.ContentStore(), descriptor)
	if err != nil {
		return nil, fmt.Errorf("failed to read image config: %w", err)
	}

	var manifest ocispec.Image
	if err := json.Unmarshal(blob, &manifest); err != nil {
		return nil, fmt.Errorf("failed to decode image config: %w", err)
	}

	opts := []oci.SpecOpts{}

	if len(manifest.Config.Env) > 0 {
		opts = append(opts, oci.WithEnv(manifest.Config.Env))
	}

	if manifest.Config.WorkingDir != "" {
		opts = append(opts, oci.WithProcessCwd(manifest.Config.WorkingDir))
	}

	// The image's entrypoint runs when the caller gives no command of its own.
	if args := append(manifest.Config.Entrypoint, manifest.Config.Cmd...); len(args) > 0 {
		opts = append(opts, oci.WithProcessArgs(args...))
	}

	return opts, nil
}

// specOpts builds the spec options tau layers on top of the image's own
// configuration: its command, environment, working directory, mounts, network
// and resource limits.
func specOpts(config *core.ContainerConfig) ([]oci.SpecOpts, error) {
	opts := []oci.SpecOpts{}

	if len(config.Command) > 0 {
		opts = append(opts, oci.WithProcessArgs(config.Command...))
	}

	// WithEnv replaces by key rather than appending, so a build that sets its
	// own PATH overrides the image's instead of shadowing it with a duplicate.
	if len(config.Env) > 0 {
		opts = append(opts, oci.WithEnv(config.Env))
	}

	if config.WorkDir != "" {
		opts = append(opts, oci.WithProcessCwd(config.WorkDir))
	}

	mounts, err := volumeMounts(config.Volumes)
	if err != nil {
		return nil, err
	}
	if len(mounts) > 0 {
		opts = append(opts, oci.WithMounts(mounts))
	}

	networkOpts, err := networkSpecOpts(config.Network)
	if err != nil {
		return nil, err
	}
	opts = append(opts, networkOpts...)

	opts = append(opts, resourceSpecOpts(config.Resources)...)

	return append(opts, privilegeSpecOpts(config.Privileges)...), nil
}

// privilegeSpecOpts widens the container's confinement. The OCI spec names
// capabilities with the CAP_ prefix, and a device needs a cgroup rule next to
// the node or the container may not open it.
func privilegeSpecOpts(privileges *core.Privileges) []oci.SpecOpts {
	if privileges == nil {
		return nil
	}

	var opts []oci.SpecOpts

	if len(privileges.Capabilities) > 0 {
		prefixed := make([]string, 0, len(privileges.Capabilities))
		for _, capability := range privileges.Capabilities {
			prefixed = append(prefixed, "CAP_"+capability)
		}
		opts = append(opts, oci.WithAddedCapabilities(prefixed))
	}

	for _, device := range privileges.Devices {
		opts = append(opts, oci.WithDevices(device, device, "rwm"))
	}

	if privileges.Unconfined {
		// AppArmor needs nothing here: unlike docker, containerd applies no
		// profile of its own unless one is asked for.
		opts = append(opts, oci.WithSeccompUnconfined)
	}

	if privileges.Privileged {
		// Deliberately not oci.WithPrivileged: it composes
		// WithAllCurrentCapabilities, which copies the capabilities of *this*
		// process. A tau talking to a system containerd is typically an
		// unprivileged client of a root daemon, and there that grants the
		// container nothing at all — the mounts then fail with "operation not
		// permitted" while the spec still claims to be privileged. What the
		// daemon may grant does not depend on the caller's own capabilities.
		opts = append(opts,
			oci.WithAllKnownCapabilities,
			oci.WithMaskedPaths(nil),
			oci.WithReadonlyPaths(nil),
			oci.WithAllDevicesAllowed,
		)
	}

	return opts
}

// volumeMounts turns the unified volume mounts into OCI bind mounts.
func volumeMounts(volumes []core.VolumeMount) ([]specs.Mount, error) {
	mounts := make([]specs.Mount, 0, len(volumes))

	for _, vol := range volumes {
		if vol.IsVolumeName {
			return nil, fmt.Errorf("named volume %q not supported by containerd: use a host path", vol.Source)
		}

		options := []string{"rbind"}
		if vol.ReadOnly {
			options = append(options, "ro")
		} else {
			options = append(options, "rw")
		}

		mounts = append(mounts, specs.Mount{
			Destination: vol.Destination,
			Type:        "bind",
			Source:      vol.Source,
			Options:     options,
		})
	}

	return mounts, nil
}

// networkSpecOpts resolves the network mode.
//
// This backend runs containerd bare, with no CNI plugin to build a bridge in a
// private network namespace. containerd's default spec unshares the network,
// which would leave a container with no route out at all, so the default here
// is to share the host's — differing from docker, which bridges by default.
// A bridged mode is refused rather than silently downgraded to one or the other.
func networkSpecOpts(network *core.NetworkConfig) ([]oci.SpecOpts, error) {
	mode := "host"
	if network != nil && network.Mode != "" {
		mode = network.Mode
	}

	if network != nil && len(network.DNS) > 0 {
		return nil, fmt.Errorf("custom DNS servers not supported by containerd: the container uses the host's resolver")
	}

	switch mode {
	case "host":
		if network != nil && len(network.PortMappings) > 0 {
			return nil, fmt.Errorf("port mappings not supported by containerd host networking: the container already shares the host's ports")
		}
		return append(resolverMount(), oci.WithHostNamespace(specs.NetworkNamespace)), nil
	case "none":
		// The default spec already unshares the network namespace.
		return nil, nil
	default:
		return nil, fmt.Errorf("network mode %q not supported by containerd: only \"host\" and \"none\" are available without CNI", mode)
	}
}

// withoutCgroups drops the cgroup the default spec asks for. Creating one under
// /sys/fs/cgroup needs privileges a rootless daemon does not have unless the
// host delegated a subtree, and asking for it anyway fails the container before
// it starts.
func withoutCgroups() oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, spec *specs.Spec) error {
		if spec.Linux != nil {
			spec.Linux.CgroupsPath = ""
		}
		return nil
	}
}

// resolverMount hands the container the host's DNS configuration.
//
// containerd, unlike docker, injects nothing: a container's /etc/resolv.conf is
// whatever its image shipped, which is usually nothing, and every lookup then
// falls back to localhost and fails. Any build that fetches dependencies needs
// this. The container shares the host's network namespace, so a resolver the
// host reaches on loopback works here too.
func resolverMount() []oci.SpecOpts {
	const resolvConf = "/etc/resolv.conf"

	if _, err := os.Stat(resolvConf); err != nil {
		return nil
	}

	return []oci.SpecOpts{oci.WithMounts([]specs.Mount{{
		Destination: resolvConf,
		Type:        "bind",
		Source:      resolvConf,
		Options:     []string{"rbind", "ro"},
	}})}
}

func resourceSpecOpts(resources *core.ResourceLimits) []oci.SpecOpts {
	if resources == nil {
		return nil
	}

	var opts []oci.SpecOpts

	if resources.Memory > 0 {
		opts = append(opts, oci.WithMemoryLimit(uint64(resources.Memory)))
	}
	if resources.PIDs > 0 {
		opts = append(opts, oci.WithPidsLimit(resources.PIDs))
	}
	if resources.CPUQuota > 0 {
		period := uint64(100000)
		if resources.CPUPeriod > 0 {
			period = uint64(resources.CPUPeriod)
		}
		opts = append(opts, oci.WithCPUCFS(resources.CPUQuota, period))
	}

	return opts
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

	tio := &taskIO{fifoDir: tmpDir}

	tio.fifoSet, err = cio.NewFIFOSetInDir(tmpDir, string(id), false)
	if err != nil {
		tio.close()
		return fmt.Errorf("failed to create FIFO set: %w", err)
	}

	// No stdin. Nothing here ever writes to the container, and a stdin FIFO that
	// is never written to and never closed never reaches EOF: any command that
	// reads it — /bin/sh, which is what many images run by default — waits for
	// input that will never come. Without the FIFO the process gets a closed
	// stdin, which is what docker gives a container nobody is attached to.
	tio.fifoSet.Stdin = ""

	tio.directIO, err = cio.NewDirectIO(ctx, tio.fifoSet)
	if err != nil {
		tio.close()
		return fmt.Errorf("failed to create DirectIO: %w", err)
	}
	tio.stdout, tio.stderr = tio.directIO.Stdout, tio.directIO.Stderr

	tio.io, err = cio.Load(tio.fifoSet)
	if err != nil {
		tio.close()
		return fmt.Errorf("failed to load IO from FIFO set: %w", err)
	}

	tio.task, err = container.NewTask(ctx, func(string) (cio.IO, error) { return tio.io, nil })
	if err != nil {
		tio.close()
		return fmt.Errorf("failed to create task for container %s: %w", id, err)
	}

	// Draining starts before the process does, so its first write already has a
	// reader on the other end of the FIFO.
	tio.drain()

	if err := tio.task.Start(ctx); err != nil {
		// A container may hold only one task, so the one that failed to start
		// has to go: leaving it makes every retry fail with "already exists".
		tio.task.Delete(ctx)
		tio.close()
		return fmt.Errorf("failed to start container %s: %w", id, err)
	}

	b.putTask(id, tio)

	return nil
}

// Stop stops a container
func (b *ContainerdBackend) Stop(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	task, err := b.resolveTask(ctx, id)
	if err != nil {
		return err
	}

	// The task is killed but deliberately not deleted. Deleting it discards the
	// runtime record, after which Inspect can no longer tell a stopped container
	// from one that never ran — it would report "created" and exit code 0, losing
	// the status that says why it was stopped — and a second Stop would fail
	// against a task that is no longer there. Deleting is Remove's job.
	if err := killAndWait(ctx, task); err != nil {
		return fmt.Errorf("failed to kill container %s: %w", id, err)
	}

	return nil
}

// resolveTask returns the task for id, preferring the one this backend started.
// The task's IO is left in place: stopping a container must not destroy the
// output that explains why it was stopped. Remove is what releases it.
func (b *ContainerdBackend) resolveTask(ctx context.Context, id core.ContainerID) (containerd.Task, error) {
	if tio, ok := b.getTask(id); ok {
		return tio.task, nil
	}

	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("failed to load container %s: %w", id, err)
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task for container %s: %w", id, err)
	}

	return task, nil
}

// killAndWait ends a running task and waits for it to exit. Containerd refuses
// to delete a task that has not exited, so SIGTERM escalates to SIGKILL.
// A task that has already exited, or that is gone entirely, is left alone.
func killAndWait(ctx context.Context, task containerd.Task) error {
	// If Status() fails, assume it is still running and try to stop it anyway.
	if status, err := task.Status(ctx); err == nil && status.Status != containerd.Running {
		return nil
	}

	// Subscribe before signalling, otherwise a task that exits promptly can
	// deliver its exit event before there is anything listening for it.
	exited, waitErr := task.Wait(ctx)

	if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
		if errdefs.IsNotFound(err) {
			// The task exited and was reaped between the status check and here.
			return nil
		}
		if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
			if errdefs.IsNotFound(err) {
				return nil
			}
			return err
		}
	}

	if waitErr != nil {
		// No exit channel to wait on, and Delete refuses a task that has not
		// exited, so fall back to watching the status.
		return waitUntilStopped(ctx, task, 8*time.Second)
	}

	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		task.Kill(ctx, syscall.SIGKILL)
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
		}
	}

	return nil
}

// waitUntilStopped polls a task's status until it is no longer running.
func waitUntilStopped(ctx context.Context, task containerd.Task, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		status, err := task.Status(ctx)
		if err != nil {
			if errdefs.IsNotFound(err) {
				return nil
			}
			return err
		}
		if status.Status != containerd.Running {
			return nil
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("task did not stop within %s: %w", timeout, ctx.Err())
		}
	}
}

// Remove removes a container
func (b *ContainerdBackend) Remove(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	container, ok := b.takeContainer(id)
	if !ok {
		var err error
		if container, err = b.client.LoadContainer(ctx, string(id)); err != nil {
			return fmt.Errorf("failed to load container %s: %w", id, err)
		}
	}

	// Remove is where the task's IO is finally released: Stop leaves it alone so
	// the output stays readable after a container is stopped.
	tio, hadTask := b.takeTask(id)
	if hadTask {
		defer tio.close()
	}

	if task, err := container.Task(ctx, nil); err == nil {
		// A task still running cannot be deleted, and Remove is how callers
		// clean up after a container that may have been left running.
		if err := killAndWait(ctx, task); err != nil {
			return fmt.Errorf("failed to kill container %s: %w", id, err)
		}
		if _, err := task.Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete task for container %s: %w", id, err)
		}
	}

	// WithSnapshotCleanup: Create gives every container its own snapshot, which
	// outlives the container and fills the disk if it is not removed with it.
	if err := container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
		return fmt.Errorf("failed to delete container %s: %w", id, err)
	}

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

	// Waiting succeeds whatever the container exited with: a non-zero status is
	// the container's own result, reported through Inspect, not a failure to wait.
	select {
	case status := <-exitStatusC:
		return status.Error()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Logs returns logs for a container
func (b *ContainerdBackend) Logs(ctx context.Context, id core.ContainerID) (io.ReadCloser, error) {
	if b.client == nil {
		return nil, fmt.Errorf("containerd client not initialized")
	}

	tio, ok := b.getTask(id)
	if !ok {
		return nil, fmt.Errorf("container %s not found or not started", id)
	}

	// The output was collected while the container ran; this waits for the
	// streams to end, which they have by the time a caller waits and then asks
	// for logs.
	collected, err := tio.collected(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to collect logs for container %s: %w", id, err)
	}

	return io.NopCloser(bytes.NewReader(collected)), nil
}

// Docker stream framing: every write is prefixed with an 8-byte header of
// {stream, 0, 0, 0, len(payload) big-endian}. containerd hands out stdout and
// stderr as two plain streams, but callers demultiplex a backend's logs with
// stdcopy, so the frames are written here to keep the two backends
// interchangeable.
const (
	streamStdout byte = 1
	streamStderr byte = 2
)

type streamFramer struct {
	mu     *sync.Mutex // shared by both streams: a frame must not be interleaved with another
	w      io.Writer
	stream byte
}

func (f *streamFramer) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var header [8]byte
	header[0] = f.stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(p)))

	if _, err := f.w.Write(header[:]); err != nil {
		return 0, err
	}

	return f.w.Write(p)
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
		// Only "there is no task" means the container was created and never
		// started. Any other failure must not be reported as a clean exit:
		// Inspect is the sole source of exit-code truth, so swallowing an RPC
		// error here would turn a failed build into a successful one.
		if !errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get task for container %s: %w", id, err)
		}
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

// Clean removes images older than age, optionally scoped to one reference.
func (b *ContainerdBackend) Clean(ctx context.Context, age time.Duration, reference string) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	imageService := b.client.ImageService()

	list, err := imageService.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}

	cutoff := time.Now().Add(-age)

	var errs []error
	for _, img := range list {
		if reference != "" && img.Name != reference {
			continue
		}
		if img.CreatedAt.After(cutoff) {
			continue
		}
		// SynchronousDelete so the content is actually released before this
		// returns: the point of the sweep is to get the disk space back.
		if err := imageService.Delete(ctx, img.Name, images.SynchronousDelete()); err != nil {
			if errdefs.IsNotFound(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("removing image %s: %w", img.Name, err))
		}
	}

	return errors.Join(errs...)
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
	// Builds run BuildKit in a container (see build.go), so this backend can
	// build wherever it can run.
	return core.BackendCapabilities{SupportsBuild: true}
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
