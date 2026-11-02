//go:build docker_integration

package artifact_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/vault"
	"github.com/taubyte/tau/services/build/artifact"
)

// needsEgressRestriction fails when this machine cannot confine a container's
// network, saying what is missing. pkg/containers refuses to start an unconfined
// build, so skipping would report builds that never ran; `make test-build` gives
// a rootful daemon.
func needsEgressRestriction(t *testing.T, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "egress") {
		t.Fatalf("this machine cannot restrict a container's egress, so no build runs here (rootful docker and CAP_NET_ADMIN are needed; use `make test-build`): %v", err)
	}
}

func TestARealBuild(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input"), []byte("hello"), 0o644))

	var log strings.Builder
	product, err := e.Make(t.Context(), artifact.Spec{
		Dir:       dir,
		Steps:     []artifact.Step{{Name: "build", Image: "busybox:1.36", Commands: []string{"echo building", "mkdir -p out", "cp input out/app.wasm"}}},
		Variables: map[string]string{"GREETING": "hi"},
		Output:    "out/app.wasm",
	}, &log)
	needsEgressRestriction(t, err)
	require.NoError(t, err)
	defer product.Close()

	body, err := io.ReadAll(product)
	require.NoError(t, err)
	require.Equal(t, "hello", string(body))
	require.Contains(t, log.String(), "building")
}

func TestARealBuildThatFails(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	var log strings.Builder
	_, err = e.Make(t.Context(), artifact.Spec{
		Dir:    t.TempDir(),
		Steps:  []artifact.Step{{Name: "build", Image: "busybox:1.36", Commands: []string{"echo compiling", "echo main.go:3: undefined: x >&2", "false", "echo this must not run > out"}}},
		Output: "out",
	}, &log)
	needsEgressRestriction(t, err)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "left nothing at",
		"it failed at the command, not at the missing output")
	require.NotContains(t, log.String(), "this must not run")
	require.Contains(t, log.String(), "compiling")
	require.Contains(t, log.String(), "main.go:3: undefined: x")
}

func TestARealBuildThatLeavesNothing(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	var log strings.Builder
	_, err = e.Make(t.Context(), artifact.Spec{
		Dir:    t.TempDir(),
		Steps:  []artifact.Step{{Name: "build", Image: "busybox:1.36", Commands: []string{"echo done"}}},
		Output: "target/app.wasm",
	}, &log)
	needsEgressRestriction(t, err)
	require.ErrorContains(t, err, "left nothing at")
}

func TestARealBuildSeesItsVariables(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	var log strings.Builder
	product, err := e.Make(t.Context(), artifact.Spec{
		Dir:       t.TempDir(),
		Steps:     []artifact.Step{{Name: "build", Image: "busybox:1.36", Commands: []string{"printf %s \"$GOPRIVATE\" > out"}}},
		Variables: map[string]string{"GOPRIVATE": "github.com/acme/*"},
		Output:    "out",
	}, &log)
	needsEgressRestriction(t, err)
	require.NoError(t, err)
	defer product.Close()

	body, err := io.ReadAll(product)
	require.NoError(t, err)
	require.Equal(t, "github.com/acme/*", string(body))
}

func TestARealBuildLogsWhileItRuns(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	dir := t.TempDir()
	log := &signal{want: "compiling", seen: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		product, err := e.Make(t.Context(), artifact.Spec{
			Dir:    dir,
			Steps:  []artifact.Step{{Name: "build", Image: "busybox:1.36", Commands: []string{"echo compiling", "while [ ! -f release ]; do sleep 0.1; done", "echo linked", "printf built > out"}}},
			Output: "out",
		}, log)
		if err == nil {
			product.Close()
		}
		done <- err
	}()

	select {
	case <-log.seen:
	case err := <-done:
		needsEgressRestriction(t, err)
		t.Fatalf("the build ended before its first line was logged: %v", err)
	case <-time.After(2 * time.Minute):
		t.Fatal("the first line must reach the log while the container runs")
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "release"), nil, 0o644))
	require.NoError(t, <-done)
	require.Contains(t, log.buf.String(), "linked")
}

// secretsAt is a build's vault holding exactly what it was granted.
type secretsAt map[string]string

func (s secretsAt) Read(_ context.Context, label string) ([]byte, error) {
	value, ok := s[label]
	if !ok {
		return nil, vault.ErrNotGranted
	}
	return []byte(value), nil
}

func TestARealBuildReadsItsSecretThroughTheHelper(t *testing.T) {
	e, err := artifact.NewEngine(nil)
	require.NoError(t, err)

	var log strings.Builder
	product, err := e.Make(t.Context(), artifact.Spec{
		Dir: t.TempDir(),
		Steps: []artifact.Step{
			{Name: "read", Image: "busybox:1.36", Commands: []string{
				"env | grep -c sk_live_1 || echo not in the environment",
				"/.taubyte/secret stripe > token",
				"echo token is $(cat token)",
				"/.taubyte/secret slack || echo refused with $?",
				"touch /.taubyte/planted || echo the vault is read-only",
			}},
			// A value served in one step is still masked in the next.
			{Name: "after", Image: "busybox:1.36", Commands: []string{"echo still $(cat token)", "cp token out"}},
		},
		Secrets: []string{"stripe"},
		Vault:   secretsAt{"stripe": "sk_live_1"},
		Output:  "out",
	}, &log)
	needsEgressRestriction(t, err)
	require.NoError(t, err, log.String())
	defer product.Close()

	body, err := io.ReadAll(product)
	require.NoError(t, err)
	require.Equal(t, "sk_live_1", string(body), "the value as stored: no newline, nothing masked")

	said := log.String()
	require.NotContains(t, said, "sk_live_1")
	require.Contains(t, said, "not in the environment")
	require.Contains(t, said, "token is ***")
	require.Contains(t, said, "still ***")
	require.Contains(t, said, "secret: vault: not granted")
	require.Contains(t, said, "refused with 1")
	require.Contains(t, said, "the vault is read-only")
}
