package container

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/invoke"
)

func jobSpec() invoke.Spec {
	s := spec()
	s.Completes = true
	return s
}

// named reports whether an environment sets name at all.
func named(env []string, name string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, name+"=") {
			return true
		}
	}
	return false
}

func TestARunGetsTheRequestOnStdinAndAnswersWithStdout(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.run = func(_ *core.ContainerConfig, stdin []byte) (string, string, int) {
		return "exported " + string(stdin), "", 0
	}
	e := newRuntime(t, rt, Image{Ref: "acme/exporter:2"}, nil)

	inst, err := e.Start(context.Background(), jobSpec())
	require.NoError(t, err)
	defer inst.Close()

	created, _, _ := rt.snapshot()
	require.Empty(t, created, "starting a job runs nothing; a request does")

	got, err := inst.Serve(context.Background(), &invoke.Request{Body: []byte("2026-10-18")})
	require.NoError(t, err)
	require.Equal(t, 200, got.Status)
	require.Equal(t, "exported 2026-10-18", string(got.Body))

	created, stopped, removed := rt.snapshot()
	require.Len(t, created, 1)
	cfg := created[0]
	require.Nil(t, cfg.Network.PortMappings, "a run listens on nothing, so nothing is published")
	require.False(t, named(cfg.Env, "PORT"), "and it is not told a port")
	require.False(t, named(cfg.Env, "AWS_LAMBDA_RUNTIME_API"), "nor where to poll")
	require.Len(t, envOf(cfg.Env, "TAU_INVOCATION_ID"), 32)
	require.True(t, cfg.Network.RestrictEgress, "a run is a stranger's code like any other")
	require.Len(t, stopped, 1)
	require.Len(t, removed, 1, "and the run's container is gone after it")
}

func TestEveryRequestIsItsOwnRun(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.run = func(cfg *core.ContainerConfig, stdin []byte) (string, string, int) {
		return envOf(cfg.Env, "TAU_INVOCATION_ID"), "", 0
	}
	e := newRuntime(t, rt, Image{Ref: "acme/exporter:2"}, nil)
	inst, err := e.Start(context.Background(), jobSpec())
	require.NoError(t, err)

	var wg sync.WaitGroup
	ids := make([]string, 2)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := inst.Serve(context.Background(), &invoke.Request{})
			require.NoError(t, err)
			ids[i] = string(got.Body)
		}()
	}
	wg.Wait()

	created, _, removed := rt.snapshot()
	require.Len(t, created, 2)
	require.Len(t, removed, 2)
	require.NotEqual(t, ids[0], ids[1])
}

func TestARunThatFailsSaysHowItEnded(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.run = func(*core.ContainerConfig, []byte) (string, string, int) {
		return "partial", "bucket receipts: access denied\n", 3
	}
	e := newRuntime(t, rt, Image{Ref: "acme/exporter:2"}, nil)
	inst, err := e.Start(context.Background(), jobSpec())
	require.NoError(t, err)

	_, err = inst.Serve(context.Background(), &invoke.Request{})
	require.ErrorIs(t, err, ErrJobFailed)
	require.ErrorContains(t, err, "exited 3")
	require.ErrorContains(t, err, "bucket receipts: access denied")

	_, _, removed := rt.snapshot()
	require.Len(t, removed, 1, "a failed run is removed too")
}

func TestAFailedRunCarriesOnlyTheEndOfStderr(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.run = func(*core.ContainerConfig, []byte) (string, string, int) {
		return "", strings.Repeat("noise\n", 10_000) + "the reason", 1
	}
	e := newRuntime(t, rt, Image{Ref: "acme/exporter:2"}, nil)
	inst, err := e.Start(context.Background(), jobSpec())
	require.NoError(t, err)

	_, err = inst.Serve(context.Background(), &invoke.Request{})
	require.ErrorContains(t, err, "the reason")
	require.Less(t, len(err.Error()), stderrTail+256)
}

func TestARunPastItsTimeoutIsKilledAndSaysSo(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.block = true
	rt.run = func(*core.ContainerConfig, []byte) (string, string, int) { return "", "", 0 }
	e := newRuntime(t, rt, Image{Ref: "acme/exporter:2"}, nil)

	s := jobSpec()
	s.Timeout = 50 * time.Millisecond
	inst, err := e.Start(context.Background(), s)
	require.NoError(t, err)

	start := time.Now()
	_, err = inst.Serve(context.Background(), &invoke.Request{})
	require.ErrorIs(t, err, ErrJobTimedOut)
	require.Less(t, time.Since(start), 5*time.Second)

	_, stopped, removed := rt.snapshot()
	require.Len(t, stopped, 1, "the run it gave up on is killed")
	require.Len(t, removed, 1)
}

func TestAJobPullsItsImageOnceAtStart(t *testing.T) {
	rt := newFakeBackend()
	rt.run = func(*core.ContainerConfig, []byte) (string, string, int) { return "", "", 0 }
	e := newRuntime(t, rt, Image{Ref: "acme/exporter:2"}, nil)

	inst, err := e.Start(context.Background(), jobSpec())
	require.NoError(t, err)
	require.Equal(t, 1, rt.pulls)

	for range 3 {
		_, err := inst.Serve(context.Background(), &invoke.Request{})
		require.NoError(t, err)
	}
	require.Equal(t, 1, rt.pulls)
}
