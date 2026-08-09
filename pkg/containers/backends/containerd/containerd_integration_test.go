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
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
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
func newTestBackend(t *testing.T) *ContainerdBackend {
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

// tarOfDockerfile packs a one-file build context.
func tarOfDockerfile(t *testing.T, dockerfile string) io.Reader {
	t.Helper()

	var buf bytes.Buffer
	writer := tar.NewWriter(&buf)

	require.NoError(t, writer.WriteHeader(&tar.Header{
		Name: "Dockerfile",
		Mode: 0o644,
		Size: int64(len(dockerfile)),
	}))
	_, err := io.WriteString(writer, dockerfile)
	require.NoError(t, err)
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

func TestHealthCheck_Integration(t *testing.T) {
	backend := newTestBackend(t)

	assert.NoError(t, backend.HealthCheck(context.Background()))
	assert.NoError(t, backend.testSocketConnection())
}
