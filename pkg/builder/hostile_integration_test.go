//go:build docker_integration && linux

package builder

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	ci "github.com/taubyte/tau/pkg/containers"
	"gotest.tools/v3/assert"
)

// hostileScript is a build.sh that goes after the node and leaves what it found
// in /out. It exits 0 so the evidence survives; each attempt records its own
// outcome. The fork loop runs last: once it hits the cap, nothing can fork.
// Run it only with the limits on: unconfined, it takes 6 GiB of the host.
const hostileScript = `
O=/out
{ test -S /var/run/docker.sock && echo SOCK || echo NOSOCK; } > $O/sock
tr '\0' '\n' < /proc/1/environ > $O/environ
find / \( -path /proc -o -path /sys \) -prune -o -name secret.txt -print 2>/dev/null > $O/secret
{ wget -q -T 3 -O /dev/null http://169.254.169.254/ && echo REACHED || echo BLOCKED; } > $O/metadata
{ wget -q -T 3 -O /dev/null http://$HOST_IP:$HOST_PORT/ && echo REACHED || echo BLOCKED; } > $O/host
grep -E '^(CapBnd|NoNewPrivs)' /proc/self/status > $O/status
for i in 1 2 3 4 5 6; do ( head -c 1073741824 /dev/zero | tail > /dev/null; echo $? >> $O/memory ) & done
wait
( i=0; while [ $i -lt 6000 ]; do sleep 300 & i=$((i+1)); echo $i > $O/forks; done )
kill -9 -1
exit 0
`

// TestHostileBuild_Integration runs a build.sh written to escape through the
// builder and checks what it got. The network and capability checks need the
// confined path (root and a rootful docker, as netguard does); where that
// fails closed (Docker Desktop, rootless), the build runs with the limits only
// and those checks are skipped.
func TestHostileBuild_Integration(t *testing.T) {
	ctx := context.Background()

	t.Setenv("TAU_NODE_SECRET", "node-secret-sentinel")
	other := filepath.Join(t.TempDir(), "other")
	assert.NilError(t, os.MkdirAll(other, 0o700))
	assert.NilError(t, os.WriteFile(filepath.Join(other, "secret.txt"), []byte("other project"), 0o600))

	ln, err := net.Listen("tcp", "0.0.0.0:0")
	assert.NilError(t, err)
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
			conn.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	src := t.TempDir()
	assert.NilError(t, os.MkdirAll(filepath.Join(src, ".taubyte"), 0o755))
	config := fmt.Sprintf(`version: "1.0"
environment:
  image: alpine:3.20
  variables:
    HOST_IP: %q
    HOST_PORT: %q
workflow:
  - build
`, outboundIP(t), port)
	assert.NilError(t, os.WriteFile(filepath.Join(src, ".taubyte", "config.yaml"), []byte(config), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(src, ".taubyte", "build.sh"), []byte(hostileScript), 0o755))

	var logs bytes.Buffer
	iface, err := New(ctx, &logs, src)
	assert.NilError(t, err)
	defer iface.Close()

	confined := true
	out, err := iface.Build()
	var outDir string
	switch {
	case err == nil:
		outDir = out.OutDir()
	case strings.Contains(err.Error(), "egress"):
		t.Logf("confined path unavailable here, running with the limits only: %v", err)
		confined = false
		outDir = runWithLimitsOnly(t, ctx, iface.(*builder))
	default:
		t.Fatalf("hostile build failed: %v\n%s", err, logs.String())
	}
	t.Cleanup(func() { os.RemoveAll(outDir) })

	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(outDir, name))
		assert.NilError(t, err, name)
		return strings.TrimSpace(string(body))
	}

	assert.Equal(t, read("sock"), "NOSOCK")
	assert.Assert(t, !strings.Contains(read("environ"), "node-secret-sentinel"), "node env leaked into the build")
	assert.Equal(t, read("secret"), "", "another project's file is visible")
	// six 1 GiB holders at once: busybox tail cannot hold one buffer past 2 GiB
	assert.Assert(t, strings.Contains(read("memory"), "137"), "6 GiB held at once was not OOM-killed: %s", read("memory"))

	forks, err := strconv.Atoi(read("forks"))
	assert.NilError(t, err)
	assert.Assert(t, forks < 4096, "fork loop reached %d processes", forks)

	if !confined {
		t.Log("skipped: metadata, host IP, capabilities, no-new-privileges (need the confined path)")
		return
	}
	assert.Equal(t, read("metadata"), "BLOCKED")
	assert.Equal(t, read("host"), "BLOCKED")

	status := read("status")
	assert.Assert(t, strings.Contains(status, "NoNewPrivs:\t1"), status)
	capBnd := strings.Fields(strings.SplitN(status, "\n", 2)[0])[1]
	caps, err := strconv.ParseUint(capBnd, 16, 64)
	assert.NilError(t, err)
	for _, bit := range []uint{19 /* SYS_PTRACE */, 21 /* SYS_ADMIN */, 27 /* MKNOD */} {
		assert.Assert(t, caps&(1<<bit) == 0, "capability %d still in the bounding set", bit)
	}
}

// runWithLimitsOnly runs the build's one step as run() does, with the limits a
// confined build gets but without the egress firewall this host cannot install.
func runWithLimitsOnly(t *testing.T, ctx context.Context, b *builder) string {
	t.Helper()
	env := b.config.HandleDepreciatedEnvironment()
	image, err := b.containerClient.Image(ctx, env.Image)
	assert.NilError(t, err)

	outDir := t.TempDir()
	ops := append(b.wd.DefaultOptions(b.config.Workflow[0], outDir, env), ci.Resources(buildLimits()))
	container, err := image.Instantiate(ctx, ops...)
	assert.NilError(t, err)
	_, err = container.Run(ctx)
	assert.NilError(t, err, "hostile build.sh exited non-zero")
	return outDir
}

// outboundIP is this host's address on its default route.
func outboundIP(t *testing.T) string {
	conn, err := net.Dial("udp", "1.1.1.1:53")
	assert.NilError(t, err)
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
