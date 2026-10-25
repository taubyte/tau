//go:build linux && containerd_integration

// Integration tests for the containerd backend. These need a real containerd —
// either a system daemon at /run/containerd/containerd.sock, or containerd plus
// rootlesskit on PATH so a rootless one can be started. Run them with
// `make test-containerd`.

package containerd

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/namespaces"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/pkg/containers/backends/conformance"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/netguard"
)

// containerd resolves image names in full, unlike docker's shorthand.
const testImage = "docker.io/library/alpine:latest"

// newTestBackend connects to whatever containerd this machine has: the system
// daemon when it is reachable, otherwise a rootless one started for the test.
// configure, if given, sets what a node's own configuration would.
func newTestBackend(t *testing.T, configure ...func(*core.ContainerdConfig)) *ContainerdBackend {
	t.Helper()

	config := core.ContainerdConfig{Namespace: "tau-test"}

	if systemContainerdReachable() {
		config.RootlessMode = core.RootlessModeDisabled
	} else {
		requireBinary(t, "containerd")
		requireBinary(t, "rootlesskit")
		config.RootlessMode = core.RootlessModeEnabled
		config.AutoStart = true
	}

	for _, set := range configure {
		set(&config)
	}

	backend, err := New(config)
	require.NoError(t, err, "containerd must be available: start it system-wide, or install containerd+rootlesskit for the rootless path")

	t.Cleanup(func() {
		if backend.client != nil && backend.client.Client != nil {
			backend.client.Close()
		}
		if backend.daemon != nil {
			backend.daemon.Stop(context.Background())
		}
	})

	return backend
}

func systemContainerdReachable() bool {
	conn, err := net.Dial("unix", "/run/containerd/containerd.sock")
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func requireBinary(t *testing.T, name string) {
	t.Helper()
	daemon := &Daemon{}
	var err error
	switch name {
	case "containerd":
		_, err = daemon.findContainerdBinary()
	case "rootlesskit":
		_, err = daemon.findRootlesskitBinary()
	}
	require.NoError(t, err, "%s must be on PATH for the rootless path", name)
}

// TestConformance_Integration is the shared suite every backend must pass. It
// is what makes docker and containerd interchangeable rather than merely
// both present.
func TestConformance_Integration(t *testing.T) {
	conformance.Run(t, conformance.Backend{
		Backend: newTestBackend(t),
		Image:   testImage,
	})
}

func TestStopRunningContainer_Integration(t *testing.T) {
	conformance.RunStopsRunningContainer(t, conformance.Backend{
		Backend: newTestBackend(t),
		Image:   testImage,
	})
}

func TestImageLifecycle_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	image := backend.Image(testImage)

	require.NoError(t, image.Pull(ctx), "pull must succeed")
	assert.True(t, image.Exists(ctx), "a pulled image must exist locally")

	digest, err := image.Digest(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, digest)

	tags, err := image.Tags(ctx)
	require.NoError(t, err)
	assert.Contains(t, tags, testImage)

	require.NoError(t, image.Remove(ctx), "remove must succeed")
	assert.False(t, image.Exists(ctx), "a removed image must be gone")

	// Create pulls what is missing, so a removed image is not a broken run.
	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image:   testImage,
		Command: []string{"/bin/true"},
	})
	require.NoError(t, err, "create must pull an image that is not present")
	require.NoError(t, backend.Remove(ctx, id))
}

func TestCreateDoesNotRepull_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()

	require.NoError(t, backend.Image(testImage).Pull(ctx))

	// With the image local, creating must not reach the registry — which also
	// means a build node keeps working when the registry does not.
	start := time.Now()
	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image:   testImage,
		Command: []string{"/bin/true"},
	})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "create must use the local image, not pull again")

	require.NoError(t, backend.Remove(ctx, id))
}

func TestRemoveReleasesResources_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()

	require.NoError(t, backend.Image(testImage).Pull(ctx))

	before := countTempFIFODirs(t)

	for i := 0; i < 5; i++ {
		id, err := backend.Create(ctx, &core.ContainerConfig{
			Image:   testImage,
			Command: []string{"/bin/sh", "-c", "echo run"},
		})
		require.NoError(t, err)
		require.NoError(t, backend.Start(ctx, id))
		require.NoError(t, backend.Wait(ctx, id))

		logs, err := backend.Logs(ctx, id)
		require.NoError(t, err)
		logs.Close()

		require.NoError(t, backend.Remove(ctx, id))
	}

	assert.Equal(t, before, countTempFIFODirs(t), "each removed container must take its FIFO directory with it")

	backend.mu.Lock()
	defer backend.mu.Unlock()
	assert.Empty(t, backend.tasks, "no task may outlive its container")
	assert.Empty(t, backend.containers, "no container handle may outlive its container")
}

func countTempFIFODirs(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(os.TempDir())
	require.NoError(t, err)

	count := 0
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 20 && e.Name()[:20] == "tau-containerd-logs-" {
			count++
		}
	}
	return count
}

// TestRestrictedEgress_Integration is the end-to-end proof of the container half
// of netguard on this backend. A restricted container must not reach a
// node-local service, must still reach the public internet, and must still
// resolve names — a workload that cannot resolve cannot fetch anything, which is
// a broken node, not a secured one. An unrestricted container sharing the
// namespace must be left alone, which is what confines the filter to the
// workloads that asked for it.
//
// It needs root (nftables) and rootful containerd.
func TestRestrictedEgress_Integration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("programming nftables needs CAP_NET_ADMIN; run this test as root")
	}
	backend := newTestBackend(t)
	if backend.isRootlessMode() {
		t.Skip("restricted egress requires rootful containerd")
	}

	ctx := context.Background()
	require.NoError(t, backend.Image(testImage).Pull(ctx))

	// A node-local service on an address the policy's CIDR list does NOT cover:
	// 203.0.113.0/24 is documentation space, so reaching it is blocked only by
	// the rule that asks the routing table whether the destination is one of
	// this host's own — the case a node with a public IP is in every day.
	const localAddr = "203.0.113.5"
	require.NoError(t, exec.Command("ip", "addr", "add", localAddr+"/32", "dev", "lo").Run(),
		"adding a test address to lo")
	t.Cleanup(func() {
		exec.Command("ip", "addr", "del", localAddr+"/32", "dev", "lo").Run()
	})

	ln, err := net.Listen("tcp", localAddr+":0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	// Addresses, not names, for the reachability probes: they must not depend on
	// the DNS probe's own result. 1.1.1.1:443 stands in for "the public internet".
	script := fmt.Sprintf(`
nc -w 3 %s %s </dev/null >/dev/null 2>&1 && echo LOCAL_REACHED || echo LOCAL_BLOCKED
nc -w 5 1.1.1.1 443 </dev/null >/dev/null 2>&1 && echo PUBLIC_REACHED || echo PUBLIC_BLOCKED
nslookup example.com >/dev/null 2>&1 && echo DNS_OK || echo DNS_FAIL
awk '/^CapEff/{print "CAPEFF=" $2}' /proc/self/status
`, localAddr, port)

	t.Cleanup(func() {
		if err := netguard.Uninstall(); err != nil {
			t.Logf("removing the egress table: %v", err)
		}
	})

	run := func(restrict bool) string {
		t.Helper()

		id, err := backend.Create(ctx, &core.ContainerConfig{
			Image:   testImage,
			Command: []string{"/bin/sh", "-c", script},
			Network: &core.NetworkConfig{RestrictEgress: restrict},
		})
		require.NoError(t, err, "create (restricted=%v) must succeed", restrict)
		defer backend.Remove(ctx, id)

		require.NoError(t, backend.Start(ctx, id))
		require.NoError(t, backend.Wait(ctx, id))

		logs, err := backend.Logs(ctx, id)
		require.NoError(t, err)
		defer logs.Close()

		var stdout, stderr bytes.Buffer
		_, err = stdcopy.StdCopy(&stdout, &stderr, logs)
		require.NoError(t, err)
		return stdout.String()
	}

	open := run(false)
	t.Logf("unrestricted baseline: %q", open)
	require.Contains(t, open, "LOCAL_REACHED",
		"the canary must be reachable with no firewall, otherwise a later block proves nothing (got %q)", open)
	require.Contains(t, open, "DNS_OK",
		"this host cannot resolve names even unrestricted, so the DNS assertion below would be meaningless (got %q)", open)

	closed := run(true)
	assert.Contains(t, closed, "LOCAL_BLOCKED",
		"a restricted container reached a node-local service (got %q)", closed)
	assert.Contains(t, closed, "PUBLIC_REACHED",
		"the firewall cut the restricted container off from the public internet (got %q)", closed)
	assert.Contains(t, closed, "DNS_OK",
		"a restricted container cannot resolve names, so it cannot fetch anything (got %q)", closed)

	// AF_PACKET writes straight to the device and never passes the output hook
	// the rule lives in, so a restricted container must not hold CAP_NET_RAW
	// (bit 13). CAP_NET_ADMIN (bit 12) is not in containerd's default set, so an
	// ordinary container must not have picked it up either.
	caps := effectiveCaps(t, closed)
	assert.Zero(t, caps&(1<<13), "a restricted container kept CAP_NET_RAW, which opens AF_PACKET (caps %#x)", caps)
	assert.Zero(t, caps&(1<<12), "a restricted container gained CAP_NET_ADMIN (caps %#x)", caps)

	// With the filter installed, a container that did not ask to be restricted
	// must be untouched: the rule matches the restricted sub-cgroup, not the
	// namespace, so ordinary workloads on the same node are not collateral.
	after := run(false)
	assert.Contains(t, after, "LOCAL_REACHED",
		"the firewall caught an unrestricted container in the same namespace (got %q)", after)
}

// TestRestrictedEgressDockerfile_Integration is the same proof one level down.
// A repository's Dockerfile is untrusted code too, and its RUN steps execute in
// runc containers BuildKit spawns — not in the container tau created. They are
// confined only because the builder is told to parent them inside the restricted
// cgroup, so this is the test that the parenting actually happened.
//
// The build itself is the other half of the assertion: it has to reach a
// registry and resolve its name through the same firewall.
func TestRestrictedEgressDockerfile_Integration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("programming nftables needs CAP_NET_ADMIN; run this test as root")
	}
	backend := newTestBackend(t)
	if backend.isRootlessMode() {
		t.Skip("restricted egress requires rootful containerd")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := netguard.Uninstall(); err != nil {
			t.Logf("removing the egress table: %v", err)
		}
	})

	// The RUN step records what it found instead of failing, so the build
	// completes either way and the verdict survives into the image.
	name := fmt.Sprintf("tau-netguard-build:%d", time.Now().UnixNano())
	dockerfile := fmt.Sprintf(`FROM %s
RUN nc -w 3 127.0.0.1 %s </dev/null >/dev/null 2>&1 && echo LOCAL_REACHED > /probe || echo LOCAL_BLOCKED > /probe
`, testImage, port)

	image := backend.Image(name)
	require.NoError(t, image.Build(ctx, &core.DockerfileBuild{
		Context:        tarOfDockerfile(t, dockerfile),
		RestrictEgress: true,
	}), "a restricted build must still reach the registry and resolve its name")
	t.Cleanup(func() { image.Remove(context.WithoutCancel(ctx)) })

	id, err := backend.Create(ctx, &core.ContainerConfig{
		Image:   name,
		Command: []string{"/bin/cat", "/probe"},
	})
	require.NoError(t, err)
	defer backend.Remove(ctx, id)

	require.NoError(t, backend.Start(ctx, id))
	require.NoError(t, backend.Wait(ctx, id))

	logs, err := backend.Logs(ctx, id)
	require.NoError(t, err)
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	_, err = stdcopy.StdCopy(&stdout, &stderr, logs)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "LOCAL_BLOCKED",
		"a Dockerfile RUN step reached a node-local service (got %q)", stdout.String())
}

// TestRestrictedBuildFetchesThroughTheFirewall_Integration: BuildKit fetches an
// ADD's URL itself, and so it does the frontend a `# syntax=` line names and the
// triggers a base image carries. Those fetches are confined because the builder
// runs inside the restricted container, which is what this proves: a URL on a
// node-local service is fetched by an unrestricted build and never reached by a
// restricted one.
func TestRestrictedBuildFetchesThroughTheFirewall_Integration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("programming nftables needs CAP_NET_ADMIN; run this test as root")
	}
	backend := newTestBackend(t)
	if backend.isRootlessMode() {
		t.Skip("restricted egress requires rootful containerd")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var fetched atomic.Int32
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched.Add(1)
		io.WriteString(w, "fetched")
	})}
	go server.Serve(ln)
	defer server.Close()

	t.Cleanup(func() {
		if err := netguard.Uninstall(); err != nil {
			t.Logf("removing the egress table: %v", err)
		}
	})

	dockerfile := fmt.Sprintf("FROM %s\nADD http://%s/x /x\n", testImage, ln.Addr())
	build := func(restrict bool) error {
		image := backend.Image(fmt.Sprintf("tau-netguard-fetch:%d", time.Now().UnixNano()))
		t.Cleanup(func() { image.Remove(context.WithoutCancel(ctx)) })
		return image.Build(ctx, &core.DockerfileBuild{Context: tarOfDockerfile(t, dockerfile), RestrictEgress: restrict})
	}

	require.NoError(t, build(false), "an unrestricted build fetches the URL")
	require.EqualValues(t, 1, fetched.Load())

	require.Error(t, build(true), "a restricted build fetched a node-local URL")
	assert.EqualValues(t, 1, fetched.Load(), "the builder of a restricted build reached a node-local service")
}

// TestABuildStaysInItsOwnCgroup_Integration: the cgroup containerd creates the
// builder in is the one its limits and, when restricted, the egress rule are
// attached to, so buildkitd and every RUN step it starts must live inside it,
// each step in a cgroup of its own. The moby/buildkit image asks buildkitd to
// move into an "init" group at the root of the hierarchy it sees, and to enable
// every controller there; with the host's hierarchy in view that is the host's
// root, which this checks is left as it was.
func TestABuildStaysInItsOwnCgroup_Integration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("reading the builder's processes and programming nftables need root; run this test as root")
	}
	backend := newTestBackend(t)
	if backend.isRootlessMode() {
		t.Skip("needs the system containerd")
	}

	t.Cleanup(func() {
		if err := netguard.Uninstall(); err != nil {
			t.Logf("removing the egress table: %v", err)
		}
	})

	for _, restrict := range []bool{false, true} {
		t.Run(fmt.Sprintf("restricted=%v", restrict), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			rootControllers := hostRootSubtreeControl(t)

			name := fmt.Sprintf("tau-own-cgroup:%d", time.Now().UnixNano())
			image := backend.Image(name)
			t.Cleanup(func() { image.Remove(context.WithoutCancel(ctx)) })

			buildCtx, stop := context.WithCancel(ctx)
			defer stop()
			built := make(chan error, 1)
			go func() {
				built <- image.Build(buildCtx, &core.DockerfileBuild{
					Context:        tarOfDockerfile(t, fmt.Sprintf("FROM %s\nRUN [\"sleep\", \"3600\"]\n", testImage)),
					RestrictEgress: restrict,
				})
			}()

			own, kit, step := runningBuild(t, ctx, backend, name, built)
			t.Logf("builder cgroup %s, buildkitd in %s, RUN step in %s", own, kit, step)

			if restrict {
				tree, err := cgroups()
				require.NoError(t, err)
				assert.True(t, cgroupWithin(own, tree.restrictedPath()), "a restricted builder was created outside the restricted subtree: %s", own)
			}
			assert.True(t, cgroupWithin(kit, own), "buildkitd left the build's cgroup %s for %s", own, kit)
			assert.True(t, cgroupWithin(step, own), "a RUN step runs outside the build's cgroup %s, in %s", own, step)
			assert.NotEqual(t, kit, step, "a RUN step has no cgroup of its own")
			assert.Equal(t, rootControllers, hostRootSubtreeControl(t), "the build changed the controllers the host's root cgroup enables")

			stop()
			assert.Error(t, <-built, "a build whose step never ends was stopped")
			assert.Equal(t, rootControllers, hostRootSubtreeControl(t), "the build changed the controllers the host's root cgroup enables")
		})
	}
}

func hostRootSubtreeControl(t *testing.T) string {
	t.Helper()
	controllers, err := os.ReadFile(filepath.Join(cgroupRoot, "cgroup.subtree_control"))
	require.NoError(t, err)
	return strings.TrimSpace(string(controllers))
}

func cgroupWithin(cgroup, ancestor string) bool {
	return cgroup == ancestor || strings.HasPrefix(cgroup, strings.TrimSuffix(ancestor, "/")+"/")
}

// runningBuild waits for the builder of image name to be running a RUN step
// that sleeps, and returns the cgroup containerd created the builder in, and
// the cgroups buildkitd and the step are in, as the host sees them.
func runningBuild(t *testing.T, ctx context.Context, backend *ContainerdBackend, name string, built <-chan error) (own, kit, step string) {
	t.Helper()
	nsCtx := namespaces.WithNamespace(ctx, backend.config.Namespace)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-built:
			t.Fatalf("the build ended before its RUN step was seen: %v", err)
		case <-ctx.Done():
			t.Fatalf("the build's RUN step never started: %v", ctx.Err())
		case <-ticker.C:
		}

		containers, err := backend.client.Containers(nsCtx)
		require.NoError(t, err)
		for _, c := range containers {
			spec, err := c.Spec(nsCtx)
			if err != nil || spec.Process == nil || !strings.Contains(strings.Join(spec.Process.Args, " "), "name="+name+",") {
				continue
			}
			task, err := c.Task(nsCtx, nil)
			if err != nil {
				continue
			}
			parents := processParents(t)
			kitPID := descendantNamed(parents, int(task.Pid()), "buildkitd")
			if kitPID == 0 {
				continue
			}
			stepPID := descendantNamed(parents, kitPID, "sleep")
			if stepPID == 0 {
				continue
			}
			return spec.Linux.CgroupsPath, processCgroup(t, kitPID), processCgroup(t, stepPID)
		}
	}
}

type process struct {
	parent int
	name   string
}

func processParents(t *testing.T) map[int]process {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	require.NoError(t, err)

	processes := map[int]process{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...; comm may hold spaces and parentheses.
		open, end := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
		if open < 0 || end < open {
			continue
		}
		fields := strings.Fields(string(stat[end+1:]))
		if len(fields) < 2 {
			continue
		}
		parent, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		processes[pid] = process{parent: parent, name: string(stat[open+1 : end])}
	}
	return processes
}

// descendantNamed returns a process called name below ancestor, or 0.
func descendantNamed(processes map[int]process, ancestor int, name string) int {
	for pid, p := range processes {
		if p.name != name {
			continue
		}
		for up := p.parent; up > 1; up = processes[up].parent {
			if up == ancestor {
				return pid
			}
		}
	}
	return 0
}

func processCgroup(t *testing.T, pid int) string {
	t.Helper()
	content, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	require.NoError(t, err)
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if own, ok := strings.CutPrefix(line, "0::"); ok {
			return own
		}
	}
	t.Fatalf("process %d has no cgroup v2 entry: %q", pid, content)
	return ""
}

// tarOfDockerfile packs a one-file build context.
func tarOfDockerfile(t *testing.T, dockerfile string) io.Reader {
	t.Helper()
	return tarOfFiles(t, map[string]string{"Dockerfile": dockerfile})
}

// tarOfFiles packs a build context of the given files, by name.
func tarOfFiles(t *testing.T, files map[string]string) io.Reader {
	t.Helper()

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)

	for name, content := range files {
		require.NoError(t, writer.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}))
		_, err := io.WriteString(writer, content)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	return &buf
}

// effectiveCaps parses the CapEff line the probe script prints.
func effectiveCaps(t *testing.T, output string) uint64 {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		hex, ok := strings.CutPrefix(strings.TrimSpace(line), "CAPEFF=")
		if !ok {
			continue
		}
		caps, err := strconv.ParseUint(hex, 16, 64)
		require.NoError(t, err, "parsing CapEff %q", hex)
		return caps
	}
	t.Fatalf("no CapEff in container output %q", output)
	return 0
}

// TestRestrictedEgressRootless_Integration is the same proof for the rootless
// daemon, which is the mode a node should prefer: there is no privileged
// container anywhere in it, and a container's traffic leaves through a userspace
// network stack, so a raw frame cannot reach a device at all. What the host sees
// are sockets belonging to the daemon, which is why the daemon is started inside
// the restricted cgroup — the rule finds them by that or not at all.
func TestRestrictedEgressRootless_Integration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("programming nftables needs CAP_NET_ADMIN; run this test as root")
	}
	requireBinary(t, "containerd")
	requireBinary(t, "rootlesskit")

	// Rootless refuses to run as root, so the daemon runs as somebody else.
	// The test still needs root for nftables, which is why this is one binary
	// driving two identities rather than two runs.
	user := os.Getenv("TAU_ROOTLESS_USER")
	if user == "" {
		t.Skip("set TAU_ROOTLESS_USER to the unprivileged user the rootless daemon should run as")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := netguard.Uninstall(); err != nil {
			t.Logf("removing the egress table: %v", err)
		}
	})

	require.NoError(t, ensureCgroupEgressFilter(), "the restricted cgroup and its rule must be installable")

	tree, err := cgroups()
	require.NoError(t, err)
	t.Logf("restricted cgroup %s at level %d, limits available: %v",
		tree.restrictedPath(), tree.level(), tree.limitsAvailable())

	// The daemon, in the restricted cgroup, as the unprivileged user.
	daemon := exec.Command("setpriv", "--reuid", user, "--regid", user, "--init-groups",
		"rootlesskit", "--net", "slirp4netns", "--disable-host-loopback", "--copy-up=/etc",
		"sh", "-c", fmt.Sprintf(
			"nc -w 3 127.0.0.1 %s </dev/null >/dev/null 2>&1 && echo LOCAL_REACHED || echo LOCAL_BLOCKED;"+
				"nc -w 5 1.1.1.1 443 </dev/null >/dev/null 2>&1 && echo PUBLIC_REACHED || echo PUBLIC_BLOCKED", port))
	daemon.Env = append(os.Environ(), "HOME=/home/"+user, "XDG_RUNTIME_DIR=/run/user/1000")

	dir, err := restrictedCgroupDir()
	require.NoError(t, err)
	defer dir.Close()
	daemon.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(dir.Fd())}

	out, err := daemon.CombinedOutput()
	require.NoError(t, err, "rootless run failed: %s", out)

	assert.Contains(t, string(out), "LOCAL_BLOCKED",
		"a rootless workload in the restricted cgroup reached a node-local service (got %q)", out)
	assert.Contains(t, string(out), "PUBLIC_REACHED",
		"the rule cut the rootless workload off from the public internet (got %q)", out)
}

func TestHealthCheck_Integration(t *testing.T) {
	backend := newTestBackend(t)

	assert.NoError(t, backend.HealthCheck(context.Background()))
	assert.NoError(t, backend.testSocketConnection())
}

// TestAnotherInstanceAdoptsAContainer_Integration is a node restarting: the
// backend that started a container is gone, and a fresh one has only the id.
func TestAnotherInstanceAdoptsAContainer_Integration(t *testing.T) {
	first := newTestBackend(t)
	ctx := context.Background()

	require.NoError(t, first.Image(testImage).Pull(ctx))
	id, err := first.Create(ctx, &core.ContainerConfig{Image: testImage, Command: []string{"/bin/sleep", "300"}})
	require.NoError(t, err)
	require.NoError(t, first.Start(ctx, id))

	second := newTestBackend(t)

	info, err := second.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "running", info.Status)

	require.NoError(t, second.Stop(ctx, id))
	info, err = second.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "stopped", info.Status)

	require.NoError(t, second.Remove(ctx, id))
	_, err = first.Inspect(ctx, id)
	assert.Error(t, err, "the container is gone for every instance")
}

func TestUnknownContainer_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()

	assert.ErrorContains(t, backend.Stop(ctx, "tau-never-created"), "failed to load container")
	assert.ErrorContains(t, backend.Remove(ctx, "tau-never-created"), "failed to load container")
	assert.ErrorContains(t, backend.Wait(ctx, "tau-never-created"), "failed to load container")
	_, err := backend.Inspect(ctx, "tau-never-created")
	assert.ErrorContains(t, err, "failed to load container")
	assert.ErrorContains(t, backend.Start(ctx, "tau-never-created"), "tau-never-created")
}

func TestCreatedButNeverStarted_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()

	require.NoError(t, backend.Image(testImage).Pull(ctx))
	id, err := backend.Create(ctx, &core.ContainerConfig{Image: testImage, Command: []string{"/bin/true"}})
	require.NoError(t, err)
	defer backend.Remove(ctx, id)

	info, err := backend.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "created", info.Status)

	assert.ErrorContains(t, backend.Stop(ctx, id), "failed to get task")
	assert.ErrorContains(t, backend.Wait(ctx, id), "failed to get task")
}

func TestMissingImage_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	image := backend.Image("docker.io/taubyte/does-not-exist:never")

	assert.False(t, image.Exists(ctx))
	assert.ErrorContains(t, image.Pull(ctx), "failed to pull image")
	assert.ErrorContains(t, image.Remove(ctx), "failed to remove image")
	_, err := image.Digest(ctx)
	assert.ErrorContains(t, err, "failed to get image")
	_, err = image.Tags(ctx)
	assert.ErrorContains(t, err, "failed to get image")

	_, err = backend.Create(ctx, &core.ContainerConfig{Image: image.Name()})
	assert.ErrorContains(t, err, "failed to pull image")
}

func TestCreateRefusesAnUnsupportedNetworkMode_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	require.NoError(t, backend.Image(testImage).Pull(ctx))

	_, err := backend.Create(ctx, &core.ContainerConfig{Image: testImage, Network: &core.NetworkConfig{Mode: "bridge"}})
	assert.ErrorContains(t, err, "failed to build OCI spec options")
}

// TestLostDaemonConnection is every operation after the connection to
// containerd is gone: each reports it, none panics.
func TestLostDaemonConnection_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	require.NoError(t, backend.client.Close())

	assert.ErrorContains(t, backend.HealthCheck(ctx), "not responding")
	assert.ErrorContains(t, backend.Clean(ctx, time.Hour, ""), "failed to list images")
	_, err := backend.Create(ctx, &core.ContainerConfig{Image: testImage})
	assert.ErrorContains(t, err, "failed to pull image")
	assert.ErrorContains(t, backend.Start(ctx, "tau-x"), "failed to load container")

	image := backend.Image(testImage)
	assert.False(t, image.Exists(ctx))
	assert.ErrorContains(t, image.Build(ctx, &core.DockerfileBuild{Context: tarOfDockerfile(t, "FROM scratch\n")}),
		"failed to create builder")
}

func TestFailingBuildReportsTheBuilderOutput_Integration(t *testing.T) {
	backend := newTestBackend(t)

	err := backend.Image("tau-test/failing-build:latest").Build(context.Background(), &core.DockerfileBuild{
		Context: tarOfDockerfile(t, "FROM "+testImage+"\nRUN echo the-step-said-this && exit 3\n"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed with exit code")
	assert.Contains(t, err.Error(), "the-step-said-this", "the builder's own output is the reason a build failed")
}

// A container whose process cannot start must be startable again with the same
// error, not "already exists": a container holds one task, and the failed one
// has to go.
func TestFailedStartCanBeRetried_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	require.NoError(t, backend.Image(testImage).Pull(ctx))

	id, err := backend.Create(ctx, &core.ContainerConfig{Image: testImage, Command: []string{"/no/such/binary"}})
	require.NoError(t, err)
	defer backend.Remove(ctx, id)

	first := backend.Start(ctx, id)
	require.Error(t, first)
	second := backend.Start(ctx, id)
	require.Error(t, second)
	assert.NotContains(t, second.Error(), "already exists")
}

func TestStartTwice_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	require.NoError(t, backend.Image(testImage).Pull(ctx))

	id, err := backend.Create(ctx, &core.ContainerConfig{Image: testImage, Command: []string{"/bin/sleep", "300"}})
	require.NoError(t, err)
	defer backend.Remove(ctx, id)

	require.NoError(t, backend.Start(ctx, id))
	assert.ErrorContains(t, backend.Start(ctx, id), "failed to create task")

	waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, backend.Wait(waitCtx, id), context.DeadlineExceeded, "Wait gives up with its context")
}

// Clean is what gets a build node its disk back: an image older than the age it
// is given goes. Scoped to one reference it touches nothing else, which is also
// what lets this prove a deletion without emptying the machine it runs on.
func TestCleanRemovesAnOldImage_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	nsCtx := namespaces.WithNamespace(ctx, backend.config.Namespace)

	image := backend.Image(testImage)
	require.NoError(t, image.Pull(ctx))
	pulled, err := backend.client.GetImage(nsCtx, testImage)
	require.NoError(t, err)

	// A name of its own for the test image, so the sweep can take nothing else.
	alias := fmt.Sprintf("docker.io/tau-test/clean:%d", time.Now().UnixNano())
	require.NoError(t, backend.nameImage(nsCtx, alias, pulled.Target()))
	t.Cleanup(func() { backend.Image(alias).Remove(ctx) })

	require.NoError(t, backend.Clean(ctx, 0, alias))

	assert.False(t, backend.Image(alias).Exists(ctx), "an image older than the age must be removed")
	assert.True(t, image.Exists(ctx), "a sweep scoped to one reference must leave every other image")
}

// A node that names a runtime for untrusted workloads gets it for every
// restricted container, and only for those. The handler named here exists on no
// node, so starting the restricted container fails, and the failure names the
// runtime containerd was asked to use.
func TestRestrictedContainersRunOnTheNamedRuntime_Integration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("programming nftables needs CAP_NET_ADMIN; run this test as root")
	}
	const runtime = "io.containerd.tau-test.v1"
	backend := newTestBackend(t, func(config *core.ContainerdConfig) { config.Runtime = runtime })
	if backend.isRootlessMode() {
		t.Skip("restricted egress requires rootful containerd")
	}

	ctx := context.Background()
	nsCtx := namespaces.WithNamespace(ctx, backend.config.Namespace)
	require.NoError(t, backend.Image(testImage).Pull(ctx))
	t.Cleanup(func() {
		if err := netguard.Uninstall(); err != nil {
			t.Logf("removing the egress table: %v", err)
		}
	})

	create := func(restrict bool) (core.ContainerID, string) {
		t.Helper()
		id, err := backend.Create(ctx, &core.ContainerConfig{
			Image:   testImage,
			Command: []string{"/bin/true"},
			Network: &core.NetworkConfig{RestrictEgress: restrict},
		})
		require.NoError(t, err)
		t.Cleanup(func() { backend.Remove(ctx, id) })

		container, err := backend.client.LoadContainer(nsCtx, string(id))
		require.NoError(t, err)
		info, err := container.Info(nsCtx)
		require.NoError(t, err)
		return id, info.Runtime.Name
	}

	restricted, named := create(true)
	assert.Equal(t, runtime, named, "a restricted container must be created on the runtime the node names")
	assert.ErrorContains(t, backend.Start(ctx, restricted), "tau-test", "starting it must go to that runtime")

	_, ordinary := create(false)
	assert.NotEqual(t, runtime, ordinary, "an ordinary container keeps containerd's default")
}

// An image whose record outlived its content — a pull or import that died
// halfway, or a content store cleaned underneath it — is refused at Create with
// the reason, rather than made into a container that cannot run.
func TestCreateRefusesAnImageWithoutContent_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()

	name := fmt.Sprintf("docker.io/tau-test/no-content:%d", time.Now().UnixNano())
	missing := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromString(name), Size: 1}
	require.NoError(t, backend.nameImage(namespaces.WithNamespace(ctx, backend.config.Namespace), name, missing))
	t.Cleanup(func() { backend.Image(name).Remove(ctx) })

	_, err := backend.Create(ctx, &core.ContainerConfig{Image: name, Command: []string{"/bin/true"}})
	assert.ErrorContains(t, err, "failed to read config of image "+name)
}

// Rebuilding under a name already in use is how an image is updated: the name
// moves to the new build rather than refusing it or keeping the old one.
func TestRebuildMovesTheName_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	name := fmt.Sprintf("tau-test/rebuilt:%d", time.Now().UnixNano())
	image := backend.Image(name)
	t.Cleanup(func() {
		image.Remove(context.Background())
		// The name BuildKit wrote into the tarball, as containerd normalised it.
		backend.Image("docker.io/" + name).Remove(context.Background())
	})

	build := func(version string) string {
		t.Helper()
		require.NoError(t, image.Build(ctx, &core.DockerfileBuild{Context: tarOfFiles(t, map[string]string{
			"Dockerfile": "FROM scratch\nCOPY version /version\n",
			"version":    version,
		})}))
		got, err := image.Digest(ctx)
		require.NoError(t, err)
		return got
	}

	first := build("one")
	assert.NotEqual(t, first, build("two"), "the name must move to the second build")
}

// Start makes the container's FIFOs in a temporary directory. When it cannot,
// it says so and leaves the container as it was, so the next Start runs it.
func TestStartWithoutATempDirectory_Integration(t *testing.T) {
	backend := newTestBackend(t)
	ctx := context.Background()
	require.NoError(t, backend.Image(testImage).Pull(ctx))

	id, err := backend.Create(ctx, &core.ContainerConfig{Image: testImage, Command: []string{"/bin/true"}})
	require.NoError(t, err)
	defer backend.Remove(ctx, id)

	t.Run("no temp directory", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
		assert.ErrorContains(t, backend.Start(ctx, id), "failed to create temp directory for FIFOs")
	})

	require.NoError(t, backend.Start(ctx, id), "the container must start once it can")
	require.NoError(t, backend.Wait(ctx, id))
	info, err := backend.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 0, info.ExitCode)
}
