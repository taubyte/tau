package scripts

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capped is scripts/cap pointed at a slice and a namespace of the test's own,
// so nothing here touches tau-cap.slice or the runs other people have in it.
// An empty ns is cap's default.
type capped struct {
	script  string
	slice   string
	ns      string
	runtime string
}

type limits struct {
	total, high, cpu string
}

var small = limits{"1G", "512M", "100%"}

func newCapped(t *testing.T) *capped {
	t.Helper()
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	if _, err := os.Stat(filepath.Join(runtime, "systemd", "private")); err != nil {
		t.Skipf("scripts/cap needs a user systemd manager: %v", err)
	}

	script, err := filepath.Abs("cap")
	require.NoError(t, err)

	id := make([]byte, 4)
	rand.Read(id)
	c := &capped{
		script:  script,
		slice:   "tau-captest-" + hex.EncodeToString(id) + ".slice",
		ns:      filepath.Join(t.TempDir(), "ns"),
		runtime: runtime,
	}
	t.Cleanup(func() {
		exec.Command("systemctl", "--user", "stop", c.slice).Run()
		exec.Command("systemctl", "--user", "revert", c.slice).Run()
	})
	return c
}

func (c *capped) cmd(ctx context.Context, l limits, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.script, append([]string{"--"}, args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TAU_CAP_") && !strings.HasPrefix(kv, "TAU_NO_CAP=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env,
		"TAU_CAP_SLICE="+c.slice,
		"TAU_CAP_TOTAL="+l.total,
		"TAU_CAP_TOTAL_HIGH="+l.high,
		"TAU_CAP_CPU="+l.cpu,
	)
	if c.ns != "" {
		cmd.Env = append(cmd.Env, "TAU_CAP_TMP="+c.ns)
	}
	return cmd
}

func (c *capped) run(t *testing.T, l limits, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := c.cmd(ctx, l, args...).CombinedOutput()
	require.NoError(t, err, "%s", out)
	return string(out)
}

// running is a capped command that stays up until it is killed.
type running struct {
	tmpdir string
	unit   string
	done   chan error
	once   sync.Once
}

// start runs script in a scope, then sleeps there. It waits until script has
// run, and says where the run's TMPDIR is and which scope it is in.
func (c *capped) start(t *testing.T, l limits, prefix []string, script string) *running {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	sh := script + `; { echo "$TMPDIR"; cat /proc/self/cgroup; } > "$0.tmp" && mv "$0.tmp" "$0" && exec sleep 300`
	cmd := c.cmd(context.Background(), l, append(prefix, "sh", "-c", sh, ready)...)
	require.NoError(t, cmd.Start())

	r := &running{done: make(chan error, 1)}
	go func() { r.done <- cmd.Wait() }()
	deadline := time.After(time.Minute)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-r.done:
			require.Failf(t, "the run ended before it was ready", "%v", err)
		case <-deadline:
			require.FailNow(t, "the run was not ready within a minute")
		case <-time.After(20 * time.Millisecond):
		}
	}

	written, err := os.ReadFile(ready)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(written)), "\n")
	r.tmpdir, r.unit = lines[0], scopeOf(t, lines[1:])
	t.Cleanup(func() { r.kill(t) })
	return r
}

// kill ends the run the way a killed test run ends, and waits until its scope
// is gone.
func (r *running) kill(t *testing.T) {
	r.once.Do(func() {
		exec.Command("systemctl", "--user", "kill", "--signal=SIGKILL", r.unit).Run()
		select {
		case <-r.done:
		case <-time.After(time.Minute):
			t.Errorf("%s outlived SIGKILL", r.unit)
		}
		require.Eventually(t, func() bool { return !active(r.unit) }, time.Minute, 20*time.Millisecond)
	})
}

func active(unit string) bool {
	out, err := exec.Command("systemctl", "--user", "show", "-p", "ActiveState", "--value", unit).Output()
	if err != nil {
		return true
	}
	state := strings.TrimSpace(string(out))
	return state != "inactive" && state != "failed"
}

// scopeOf finds the scope in /proc/self/cgroup's unified line.
func scopeOf(t *testing.T, cgroup []string) string {
	t.Helper()
	for _, line := range cgroup {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Base(path)
		}
	}
	require.Failf(t, "no unified cgroup", "%q", cgroup)
	return ""
}

func (c *capped) limits(t *testing.T) limits {
	t.Helper()
	out, err := exec.Command("systemctl", "--user", "show", c.slice,
		"-p", "MemoryMax", "-p", "MemoryHigh", "-p", "CPUQuotaPerSecUSec").Output()
	require.NoError(t, err)
	var l limits
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		key, value, _ := strings.Cut(scanner.Text(), "=")
		switch key {
		case "MemoryMax":
			l.total = value
		case "MemoryHigh":
			l.high = value
		case "CPUQuotaPerSecUSec":
			l.cpu = value
		}
	}
	return l
}

func TestARunHasATMPDIRNamedForItsScope(t *testing.T) {
	c := newCapped(t)

	out := c.run(t, small, "sh", "-c", `echo "$TMPDIR"; cat /proc/self/cgroup; ls /proc/self/fd`)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	tmpdir, scope := lines[0], scopeOf(t, lines[1:])
	id, ok := strings.CutPrefix(strings.TrimSuffix(scope, ".scope"), "tau-cap-")
	require.True(t, ok, "the run is in %s", scope)
	assert.Equal(t, filepath.Join(c.ns, id), tmpdir)
	assert.Contains(t, out, "/"+c.slice+"/"+scope)

	info, err := os.Stat(tmpdir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	for _, fd := range lines {
		assert.NotEqual(t, "9", fd, "the command must not hold the lock cap took")
	}
}

// TestTheDefaultTMPDIRHasRoomForASocketInATempDir binds a socket as deep as
// t.TempDir can put one: a 64-byte test name, the widest random suffix and the
// sequence directory, with the longest socket name a test here binds. The run
// is in the real namespace, so it stays alive while the socket is bound, which
// is what keeps another cap call from removing its directory.
func TestTheDefaultTMPDIRHasRoomForASocketInATempDir(t *testing.T) {
	c := newCapped(t)
	c.ns = ""

	r := c.start(t, small, nil, "true")
	assert.Regexp(t, fmt.Sprintf(`^/tmp/cap(-%d)?/[0-9]+$`, os.Getuid()), r.tmpdir)

	deepest := filepath.Join(r.tmpdir, strings.Repeat("T", 64)+"4294967295", "001")
	require.NoError(t, os.MkdirAll(deepest, 0o700))
	listener, err := net.Listen("unix", filepath.Join(deepest, "containerd.sock"))
	require.NoError(t, err)
	listener.Close()
}

func TestTheSliceIsOnlyLoweredWhenNothingIsInIt(t *testing.T) {
	c := newCapped(t)
	big := limits{"2G", "1G", "200%"}
	const gib, mib = 1 << 30, 1 << 20

	c.run(t, big, "true")
	assert.Equal(t, limits{fmt.Sprint(2 * gib), fmt.Sprint(gib), "2s"}, c.limits(t))

	c.run(t, small, "true")
	assert.Equal(t, limits{fmt.Sprint(gib), fmt.Sprint(512 * mib), "1s"}, c.limits(t), "an idle slice takes what is asked")

	inside := c.start(t, big, nil, "true")
	assert.Equal(t, limits{fmt.Sprint(2 * gib), fmt.Sprint(gib), "2s"}, c.limits(t))

	c.run(t, small, "true")
	assert.Equal(t, limits{fmt.Sprint(2 * gib), fmt.Sprint(gib), "2s"}, c.limits(t), "a busy slice is never lowered")

	c.run(t, limits{"3G", "512M", "300%"}, "true")
	assert.Equal(t, limits{fmt.Sprint(3 * gib), fmt.Sprint(gib), "3s"}, c.limits(t), "each limit only rises")

	inside.kill(t)
	c.run(t, small, "true")
	assert.Equal(t, limits{fmt.Sprint(gib), fmt.Sprint(512 * mib), "1s"}, c.limits(t))
}

func TestAKilledRunsTMPDIRIsRemovedByTheNextRun(t *testing.T) {
	c := newCapped(t)
	keepsData := `echo data > "$TMPDIR/data"`
	killed := c.start(t, small, nil, keepsData)
	alive := c.start(t, small, nil, keepsData)

	stale := filepath.Join(c.ns, "999999")
	notARun := filepath.Join(c.ns, "keep")
	outside := filepath.Join(t.TempDir(), "1")
	for _, dir := range []string{stale, notARun, outside} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "data"), []byte("data"), 0o600))
	}

	killed.kill(t)
	require.FileExists(t, filepath.Join(killed.tmpdir, "data"), "a killed run takes nothing with it")

	next := c.run(t, small, "sh", "-c", `echo "$TMPDIR"`)

	// The next run may be given the killed run's number, and a fresh directory
	// under it.
	assert.NoFileExists(t, filepath.Join(killed.tmpdir, "data"))
	if strings.TrimSpace(next) != killed.tmpdir {
		assert.NoDirExists(t, killed.tmpdir)
	}
	assert.NoDirExists(t, stale, "a directory whose scope is gone is removed")
	assert.FileExists(t, filepath.Join(alive.tmpdir, "data"), "a live run keeps its TMPDIR")
	assert.FileExists(t, filepath.Join(notARun, "data"), "only a scope's directory is ever removed")
	assert.FileExists(t, filepath.Join(outside, "data"), "nothing outside the namespace is removed")
}

// TestACallWaitsWhileTheLockIsHeld holds the lock every cap call takes, as a
// call does from looking at the other runs until its own is in its scope.
func TestACallWaitsWhileTheLockIsHeld(t *testing.T) {
	c := newCapped(t)
	lock, err := os.OpenFile(filepath.Join(c.runtime, "tau-cap.lock"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	defer lock.Close()
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_EX))

	ran := filepath.Join(t.TempDir(), "ran")
	cmd := c.cmd(t.Context(), small, "touch", ran)
	require.NoError(t, cmd.Start())
	time.Sleep(time.Second)
	assert.NoFileExists(t, ran, "a call does nothing until it holds the lock")

	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_UN))
	require.NoError(t, cmd.Wait())
	assert.FileExists(t, ran)
}

// TestRunsStartedTogetherEachKeepTheirTMPDIR starts runs side by side. Each
// claims its TMPDIR and checks a second later that the claim is still there:
// a directory removed under a run, or given to two runs at once, fails it.
func TestRunsStartedTogetherEachKeepTheirTMPDIR(t *testing.T) {
	c := newCapped(t)

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			out, err := c.cmd(ctx, small, "sh", "-c",
				`echo $$ > "$TMPDIR/owner" && sleep 1 && test "$(cat "$TMPDIR/owner")" = $$`).CombinedOutput()
			assert.NoError(t, err, "%s", out)
		})
	}
	wg.Wait()
}

func TestTheCommandGetsItsArgumentsAsGiven(t *testing.T) {
	c := newCapped(t)

	out := c.run(t, small, "sh", "-c", `x=shell; echo "${x} ${NOT_SET_ANYWHERE:-default}"`)

	assert.Equal(t, "shell default\n", out)
}

func TestACappedRunCanCapAnother(t *testing.T) {
	c := newCapped(t)

	out := c.run(t, small, "sh", "-c", `echo "$TMPDIR"; "$0" -- sh -c 'echo "$TMPDIR"'`, c.script)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2, out)
	assert.NotEqual(t, lines[0], lines[1])
	assert.Equal(t, c.ns, filepath.Dir(lines[1]))
}
