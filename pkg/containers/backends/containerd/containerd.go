//go:build linux

package containerd

import (
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
	"strings"
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
	"github.com/taubyte/tau/pkg/netguard"
)

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
	fifoDir  string
	directIO *cio.DirectIO
	io       cio.IO

	// logs holds the container's output, docker-framed, collected as it is
	// produced. grew is closed and replaced on every append, and done closes
	// once both streams have hit EOF, so a follower blocks on either.
	//
	// The whole output is held in memory; spill to fifoDir if a workload ever
	// outgrows that.
	logsMu  sync.Mutex
	logs    []byte
	grew    chan struct{}
	done    chan struct{}
	drained sync.WaitGroup
}

// drain starts copying the task's output the moment it starts running.
//
// containerd keeps no log store: these FIFOs are the only sink, and a container
// that fills their pipe buffer — roughly 64KiB — blocks on write until someone
// reads. Collecting only when Logs is called therefore wedges any container
// that says more than that, which is every real build.
func (t *taskIO) drain() {
	t.grew = make(chan struct{})
	t.done = make(chan struct{})
	type drainedStream struct {
		reader io.Reader
		stream byte
	}
	for _, s := range []drainedStream{
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
	go func() {
		t.drained.Wait()
		close(t.done)
	}()
}

// logSink appends to a taskIO's collected output. The framer already holds
// logsMu when it writes.
type logSink taskIO

func (s *logSink) Write(p []byte) (int, error) {
	s.logs = append(s.logs, p...)
	close(s.grew)
	s.grew = make(chan struct{})
	return len(p), nil
}

// follower reads a task's output from the start, blocking at the end of what
// has arrived until more does or both streams end.
type follower struct {
	ctx    context.Context
	t      *taskIO
	offset int
}

func (f *follower) Read(p []byte) (int, error) {
	for {
		f.t.logsMu.Lock()
		if f.offset < len(f.t.logs) {
			n := copy(p, f.t.logs[f.offset:])
			f.offset += n
			f.t.logsMu.Unlock()
			return n, nil
		}
		grew := f.t.grew
		f.t.logsMu.Unlock()

		select {
		case <-grew:
		case <-f.t.done:
			f.t.logsMu.Lock()
			more := f.offset < len(f.t.logs)
			f.t.logsMu.Unlock()
			if !more {
				return 0, io.EOF
			}
		case <-f.ctx.Done():
			return 0, f.ctx.Err()
		}
	}
}

type ContainerdBackend struct {
	config core.ContainerdConfig
	client *Client
	daemon *Daemon

	// mu guards tasks and containers: a backend is shared across goroutines
	// (one per build step), and Create/Start/Stop/Remove all mutate both.
	mu         sync.Mutex
	tasks      map[core.ContainerID]*taskIO
	containers map[core.ContainerID]containerd.Container

	// stdins are what Start writes to the containers created with one: a task's
	// IO is made at Start, so the stream waits here from Create.
	stdins map[core.ContainerID]io.Reader
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

// defaultNamespace is the containerd namespace tau works in when a caller does
// not name one. It cannot be empty: containerd requires a namespace on every
// call, and the egress firewall derives the cgroup it matches from it, so an
// unset one fails a restricted container closed.
const defaultNamespace = "taubyte"

// withDefaults fills in what a caller left unset. Callers build a
// core.ContainerdConfig literal (pkg/containers/backend_manager.go), so an
// unnamed field is the common case, not an edge one.
func withDefaults(config core.ContainerdConfig) core.ContainerdConfig {
	if config.Namespace == "" {
		config.Namespace = defaultNamespace
	}
	return config
}

func New(config core.ContainerdConfig) (*ContainerdBackend, error) {
	backend := &ContainerdBackend{
		config:     withDefaults(config),
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

func (b *ContainerdBackend) detectRootlessMode() error {
	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	isRoot := currentUser.Uid == "0"

	if b.config.RootlessMode != core.RootlessModeAuto {
		if b.config.RootlessMode == core.RootlessModeEnabled && isRoot {
			return fmt.Errorf("cannot enable rootless mode when running as root")
		}
		if b.config.RootlessMode == core.RootlessModeDisabled && !isRoot {
			// Allow running in "disabled" mode even as non-root - we'll assume system containerd
		}
		return nil
	}

	if isRoot {
		b.config.RootlessMode = core.RootlessModeDisabled
	} else {
		b.config.RootlessMode = core.RootlessModeEnabled
	}

	return nil
}

func (b *ContainerdBackend) isRootlessMode() bool {
	return b.config.RootlessMode == core.RootlessModeEnabled
}

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

	if b.config.AutoStart && b.isRootlessMode() {
		return b.daemon.Start(ctx)
	}

	if b.config.RootlessMode == core.RootlessModeDisabled {
		return fmt.Errorf("containerd not running at system socket %s - please start containerd system-wide", socketPath)
	}

	return fmt.Errorf("containerd socket not found at %s and AutoStart is disabled", socketPath)
}

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

func (b *ContainerdBackend) BackendType() core.BackendType {
	return core.BackendTypeContainerd
}

func (b *ContainerdBackend) Image(name string) core.Image {
	return &containerdImage{
		backend: b,
		name:    name,
	}
}

func (b *ContainerdBackend) Create(ctx context.Context, config *core.ContainerConfig) (core.ContainerID, error) {
	return b.create(ctx, config)
}

// create creates a container with extra spec options layered last, for what
// only this backend's own containers ask for.
func (b *ContainerdBackend) create(ctx context.Context, config *core.ContainerConfig, extra ...oci.SpecOpts) (_ core.ContainerID, err error) {
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

	// A container is placed in tau's own cgroup subtree whenever there is one:
	// that is what the egress rule matches, and what carries resource limits.
	// Rootless is no longer the exception it was — with the subtree delegated,
	// a rootless daemon owns cgroups just as a rootful one does.
	restricted := config.Network != nil && config.Network.RestrictEgress

	var resolv string
	if restricted {
		if resolv, err = restrictedResolvConf(resolvDirOf(containerID), "/etc/resolv.conf"); err != nil {
			return "", err
		}
		// A container that was not created has no Remove to take its file back.
		defer func() {
			if err != nil {
				os.RemoveAll(resolvDirOf(containerID))
			}
		}()
	}

	opts, err := specOpts(config, resolv)
	if err != nil {
		return "", fmt.Errorf("failed to build OCI spec options: %w", err)
	}
	cgroupPath, cgroupErr := containerCgroupPath(string(containerID), restricted)

	if config.Resources != nil && !resourceLimitsAvailable() {
		return "", fmt.Errorf("resource limits need tau's cgroup subtree, which is not available here (a systemd unit with Delegate=yes, or root): %v", cgroupErr)
	}

	if cgroupErr != nil {
		if restricted {
			return "", fmt.Errorf("restricted egress needs tau's cgroup subtree: %w", cgroupErr)
		}
		opts = append(opts, withoutCgroups())
	} else {
		opts = append(opts, oci.WithCgroup(cgroupPath))
	}

	if restricted {
		// Install, or re-assert, the single rule covering tau's restricted
		// subtree. Fail-closed: no firewall, no container. Installing here —
		// before the container is created — guarantees the rule is in place well
		// before the workload starts.
		if err := ensureCgroupEgressFilter(); err != nil {
			return "", fmt.Errorf("installing egress firewall (container not created): %w", err)
		}
	}

	// The image's own configuration is the baseline, exactly as docker treats
	// it, and tau's options are layered on top.
	imageOpts, err := imageSpecOpts(ctx, image)
	if err != nil {
		return "", fmt.Errorf("failed to read config of image %s: %w", config.Image, err)
	}

	newContainer := []containerd.NewContainerOpts{
		containerd.WithImage(image),
		containerd.WithNewSnapshot(fmt.Sprintf("%s-snapshot", containerID), image),
		containerd.WithNewSpec(append(append(imageOpts, opts...), extra...)...),
	}

	// The runtime is the boundary an untrusted workload runs against, so it is
	// named for restricted ones when the node has been given one. Everything
	// else keeps containerd's default.
	if restricted && b.config.Runtime != "" {
		newContainer = append(newContainer, containerd.WithRuntime(b.config.Runtime, nil))
	}

	container, err := b.client.NewContainer(ctx, string(containerID), newContainer...)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	b.putContainer(containerID, container)
	if config.Stdin != nil {
		b.mu.Lock()
		if b.stdins == nil {
			b.stdins = map[core.ContainerID]io.Reader{}
		}
		b.stdins[containerID] = config.Stdin
		b.mu.Unlock()
	}

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

// specOpts is the container's spec; resolv is its own restricted resolver file,
// written when its egress is restricted.
func specOpts(config *core.ContainerConfig, resolv string) ([]oci.SpecOpts, error) {
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

	networkOpts, err := networkSpecOpts(config.Network, resolv)
	if err != nil {
		return nil, err
	}
	opts = append(opts, networkOpts...)

	opts = append(opts, resourceSpecOpts(restrictedResources(config))...)
	opts = append(opts, privilegeSpecOpts(config.Privileges)...)

	if config.Network != nil && config.Network.RestrictEgress && !privileged(config.Privileges) {
		// Last, so it also takes back what the privileges above handed out.
		//
		// CAP_NET_RAW lets a container bypass an IP-layer filter: AF_PACKET writes straight
		// to the device, skipping the output hook the rule lives in, and a restricted
		// container shares the host's network namespace. Dropped from the bounding set, it
		// is denied to everything the container spawns.
		//
		// Not for privileged containers: the only one is the builder, and BuildKit's per-step
		// runc needs CAP_NET_ADMIN for the cgroup device filter and CAP_NET_RAW in each
		// step's default set. So a Dockerfile's RUN steps are confined by the cgroup rule but
		// keep AF_PACKET; a network namespace per step would close that.
		opts = append(opts, oci.WithDroppedCapabilities([]string{"CAP_NET_RAW"}))
	}

	return opts, nil
}

func privileged(privileges *core.Privileges) bool {
	return privileges != nil && privileges.Privileged
}

// defaultRestrictedPIDs caps how many processes untrusted code may create. A
// build that forks until the node falls over needs no exploit and no network,
// and it takes the whole node's other tenants with it. High enough that no real
// build notices; low enough that a loop does.
const defaultRestrictedPIDs = 4096

// restrictedResources gives an untrusted workload a process cap when the caller
// asked for no limits of its own. Memory is deliberately not guessed at here —
// a wrong number fails real builds, and it belongs in configuration.
func restrictedResources(config *core.ContainerConfig) *core.ResourceLimits {
	if config.Network == nil || !config.Network.RestrictEgress || !resourceLimitsAvailable() {
		return config.Resources
	}

	if config.Resources == nil {
		return &core.ResourceLimits{PIDs: defaultRestrictedPIDs}
	}
	if config.Resources.PIDs == 0 {
		limits := *config.Resources
		limits.PIDs = defaultRestrictedPIDs
		return &limits
	}
	return config.Resources
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

// portMappings is the config's mappings, or nothing when it declares no network.
func portMappings(network *core.NetworkConfig) []core.PortMapping {
	if network == nil {
		return nil
	}
	return network.PortMappings
}

// networkSpecOpts resolves the network mode. containerd runs here without CNI, and
// its default spec unshares the network, leaving no route out, so the default is
// the host's network (docker bridges by default). A bridged mode is refused rather
// than silently downgraded.
func networkSpecOpts(network *core.NetworkConfig, resolv string) ([]oci.SpecOpts, error) {
	mode := "host"
	if network != nil && network.Mode != "" {
		mode = network.Mode
	}

	if network != nil && len(network.DNS) > 0 {
		return nil, fmt.Errorf("custom DNS servers not supported by containerd: the container uses the host's resolver")
	}

	switch mode {
	case "host":
		// Host networking already puts the container on the host's ports, so only a mapping
		// to a different host port, a real translation, is refused.
		for _, m := range portMappings(network) {
			if m.HostPort != m.ContainerPort {
				return nil, fmt.Errorf(
					"containerd host networking cannot map port %d to %d: the container already shares the host's ports",
					m.ContainerPort, m.HostPort)
			}
		}
		resolver, err := resolverMount(network != nil && network.RestrictEgress, resolv)
		if err != nil {
			return nil, err
		}
		return append(resolver, oci.WithHostNamespace(specs.NetworkNamespace)), nil
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

// resolverMount hands the container the DNS configuration it should use.
//
// containerd, unlike docker, injects nothing: a container's /etc/resolv.conf is
// whatever its image shipped, which is usually nothing, and every lookup then
// falls back to localhost and fails. Any build that fetches dependencies needs
// this. The container shares the host's network namespace, so a resolver the
// host reaches on loopback works here too — unless egress is restricted, in
// which case the host's file has to be filtered first (restrictedResolvConf): resolv
// is that filtered file.
func resolverMount(restricted bool, resolv string) ([]oci.SpecOpts, error) {
	const resolvConf = "/etc/resolv.conf"

	source := resolvConf
	if restricted {
		if resolv == "" {
			return nil, errors.New("a restricted container has no resolver file of its own")
		}
		source = resolv
	} else if _, err := os.Stat(resolvConf); err != nil {
		return nil, nil
	}

	return []oci.SpecOpts{oci.WithMounts([]specs.Mount{{
		Destination: resolvConf,
		Type:        "bind",
		Source:      source,
		Options:     []string{"rbind", "ro"},
	}})}, nil
}

// publicResolvers are the last resort for a restricted container: reached only
// when no resolver the host is configured with is one the container is allowed
// to talk to. A restricted workload resolves public names, so a public resolver
// is the right shape of answer — and it cannot resolve the node's internal
// names, which is the point.
var publicResolvers = []string{"1.1.1.1", "8.8.8.8"}

// restrictedResolvConf writes, in dir, and returns the path of, the /etc/resolv.conf
// a restricted container gets: the host's nameservers minus every one the egress
// policy denies.
//
// Without this the firewall quietly costs the container its name resolution. The
// default resolver on a systemd host is the resolved stub at 127.0.0.53, which
// is loopback and therefore denied, so a restricted container inheriting the
// host file would resolve nothing and every fetch would fail — a broken node,
// not a secured one. systemd publishes the real upstream servers separately, so
// those are tried before falling back to public ones.
//
// The file is the container's own, written once: a file shared by every container
// was rewritten under the ones already running, which could read it empty.
func restrictedResolvConf(dir, hostPath string) (string, error) {
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating the restricted container's resolver directory: %w", err)
	}

	servers := permittedNameservers(hostPath)
	if len(servers) == 0 {
		// The stub-resolver case: ask systemd for what it forwards to.
		servers = permittedNameservers("/run/systemd/resolve/resolv.conf")
	}
	if len(servers) == 0 {
		servers = publicResolvers
	}

	var b strings.Builder
	b.WriteString("# generated by tau: the host's resolvers, minus those a restricted build may not reach\n")
	for _, s := range servers {
		fmt.Fprintf(&b, "nameserver %s\n", s)
	}

	path := filepath.Join(dir, "resolv.conf")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("writing the restricted build's resolv.conf: %w", err)
	}
	return path, nil
}

// permittedNameservers returns the nameservers in a resolv.conf-shaped file
// that the egress policy allows a restricted container to reach. A missing or
// unreadable file is not an error here — the caller has another candidate.
func permittedNameservers(path string) []string {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var servers []string
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		if ip := net.ParseIP(fields[1]); ip != nil && !netguard.IsDenied(ip) {
			servers = append(servers, fields[1])
		}
	}
	return servers
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

	b.mu.Lock()
	stdin, feed := b.stdins[id]
	delete(b.stdins, id)
	b.mu.Unlock()

	// No stdin unless the container was given one. A stdin FIFO that is never
	// written to and never closed never reaches EOF: any command that reads it —
	// /bin/sh, which is what many images run by default — waits for input that
	// will never come. Without the FIFO the process gets a closed stdin, which is
	// what docker gives a container nobody is attached to.
	if !feed {
		tio.fifoSet.Stdin = ""
	}

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

	// THE PROCESS SEES EOF WHEN THE COPY ENDS — but not from closing the FIFO:
	// the shim keeps a handle of its own on it, so the process sees EOF only
	// when the task is told to close its stdin, which is what `ctr` does too.
	// The context is the backend's: Start's may end before the copy does.
	if feed {
		task := tio.task
		go func() {
			io.Copy(tio.directIO.Stdin, stdin)
			tio.directIO.Stdin.Close()
			task.CloseIO(namespaces.WithNamespace(context.Background(), b.config.Namespace), containerd.WithStdinCloser)
		}()
	}

	return nil
}

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
	return os.RemoveAll(resolvDirOf(id))
}

func (b *ContainerdBackend) Wait(ctx context.Context, id core.ContainerID) error {
	if b.client == nil {
		return fmt.Errorf("containerd client not initialized")
	}

	ctx = namespaces.WithNamespace(ctx, b.config.Namespace)

	container, err := b.client.LoadContainer(ctx, string(id))
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, ended(ctx, err))
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get task for container %s: %w", id, ended(ctx, err))
	}

	exitStatusC, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for container %s: %w", id, ended(ctx, err))
	}

	// Waiting succeeds whatever the container exited with: a non-zero status is
	// the container's own result, reported through Inspect, not a failure to wait.
	select {
	case status := <-exitStatusC:
		return ended(ctx, status.Error())
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ended is err, or ctx's own error once ctx has ended. containerd ends a call
// its context cancelled with a status of its own that does not wrap the
// context's error, and the exit channel carries that status as soon as the
// context ends, so a select can see it before ctx.Done: a caller that set a
// deadline must still get the deadline back. gRPC reports a call the server
// cut as DeadlineExceeded once the deadline has passed by the clock, which can
// be before the context's own timer has fired and set its error.
func ended(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if d, ok := ctx.Deadline(); ok && !d.After(time.Now()) {
		return context.DeadlineExceeded
	}
	return err
}

func (b *ContainerdBackend) Logs(ctx context.Context, id core.ContainerID) (io.ReadCloser, error) {
	if b.client == nil {
		return nil, fmt.Errorf("containerd client not initialized")
	}

	tio, ok := b.getTask(id)
	if !ok {
		return nil, fmt.Errorf("container %s not found or not started", id)
	}

	return io.NopCloser(&follower{ctx: ctx, t: tio}), nil
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

func (b *ContainerdBackend) Capabilities() core.BackendCapabilities {
	// Builds run BuildKit in a container (see build.go), so this backend can
	// build wherever it can run.
	return core.BackendCapabilities{SupportsBuild: true}
}

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

// resolvDirOf is where a restricted container's resolver file lives: named by the
// container, so Remove finds it whichever process created the container.
func resolvDirOf(id core.ContainerID) string {
	return filepath.Join(os.TempDir(), "tau-resolv-"+string(id))
}
