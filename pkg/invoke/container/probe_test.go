package container

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"sync"
	syncatomic "sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// probe_test holds the two things a workload declares about itself that this
// tier applies: a CPU ceiling, and an HTTP readiness check.

// TestACPUCeilingIsACFSQuota: 250 millicores is 25ms of CPU in every 100ms.
func TestACPUCeilingIsACFSQuota(t *testing.T) {
	e, err := New(ImageFromSpec, WithBackend(newFakeBackend()))
	require.NoError(t, err)

	cfg := e.config(Image{Ref: "acme/shop:1", CPU: 250}, nil, 0, "", nil)
	require.NotNil(t, cfg.Resources)
	require.Equal(t, int64(100_000), cfg.Resources.CPUPeriod)
	require.Equal(t, int64(25_000), cfg.Resources.CPUQuota)
	require.Zero(t, cfg.Resources.Memory, "a CPU ceiling alone says nothing about memory")

	// NEITHER DECLARED IS NO LIMIT AT ALL, rather than a zero quota — which a
	// backend would read as no CPU.
	require.Nil(t, e.config(Image{Ref: "acme/shop:1"}, nil, 0, "", nil).Resources)
}

// TestTheSpecsCeilingReachesTheImage, which is what the default resolver hands
// the backend.
func TestTheSpecsCeilingReachesTheImage(t *testing.T) {
	img, err := ImageFromSpec(invoke.Spec{Artifact: "acme/shop:1", CPU: 500, Memory: 64 << 20})
	require.NoError(t, err)
	require.Equal(t, int64(500), img.CPU)
	require.Equal(t, int64(64<<20), img.Memory)
}

// answering is a "container" whose path answers `status()` and records how
// often it was asked.
func answering(asked *syncatomic.Int32, status func(path string) int) func(port int) (closer, error) {
	return func(port int) (closer, error) {
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			return nil, err
		}
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked.Add(1)
			w.WriteHeader(status(r.URL.Path))
		})}
		go srv.Serve(l)
		return closerFunc(srv.Close), nil
	}
}

// TestADeclaredProbeIsAskedUntilItAnswers. An accepted connection is not
// enough when the author named a path: a server that is up and still warming
// answers 503, and an instance handed work then fails it.
func TestADeclaredProbeIsAskedUntilItAnswers(t *testing.T) {
	backend := newFakeBackend()
	var asked syncatomic.Int32
	backend.listen = answering(&asked, func(path string) int {
		if path != "/healthz" {
			return http.StatusNotFound
		}
		if asked.Load() < 4 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	})
	e := newRuntime(t, backend, Image{Ref: "acme/shop:1"}, nil)

	s := spec()
	s.Readiness = &invoke.Probe{Path: "/healthz"}
	inst, err := e.Start(t.Context(), s)
	require.NoError(t, err)
	defer inst.Close()
	require.GreaterOrEqual(t, asked.Load(), int32(4), "ready only once the path answered 2xx")
}

// TestAProbeThatNeverAnswersIsNotReady, within the budget.
func TestAProbeThatNeverAnswersIsNotReady(t *testing.T) {
	backend := newFakeBackend()
	var asked syncatomic.Int32
	backend.listen = answering(&asked, func(string) int { return http.StatusServiceUnavailable })
	e := newRuntime(t, backend, Image{Ref: "acme/shop:1"}, func() []Option {
		return []Option{WithReadyTimeout(200 * time.Millisecond)}
	})

	s := spec()
	s.Readiness = &invoke.Probe{Path: "/healthz"}
	_, err := e.Start(t.Context(), s)
	require.ErrorIs(t, err, ErrNotReady)
	require.Positive(t, asked.Load(), "the listener was up — it was the probe that said no")
}

// TestTheProbeAsksTheNamedPort and not whichever sorted first: `admin` sorts
// before `http`, and only `admin` says ready.
func TestTheProbeAsksTheNamedPort(t *testing.T) {
	backend := newFakeBackend()
	var (
		mu    sync.Mutex
		order []int
	)
	backend.listen = func(port int) (closer, error) {
		mu.Lock()
		order = append(order, port)
		first := len(order) == 1
		mu.Unlock()
		var asked syncatomic.Int32
		return answering(&asked, func(string) int {
			if first {
				return http.StatusServiceUnavailable // admin, the one NOT named
			}
			return http.StatusOK
		})(port)
	}
	e := newRuntime(t, backend, Image{Ref: "acme/shop:1"}, func() []Option {
		return []Option{WithReadyTimeout(time.Second)}
	})

	s := spec()
	s.Ports = map[string]int{"admin": 9090, "http": 8080}
	s.Readiness = &invoke.Probe{Path: "/ready", Port: "http"}
	inst, err := e.Start(t.Context(), s)
	require.NoError(t, err)
	inst.Close()

	// AND A PORT THAT WAS NEVER DECLARED IS NEVER READY, rather than probed
	// somewhere else.
	s.Readiness = &invoke.Probe{Path: "/ready", Port: "metrics"}
	_, err = e.Start(context.Background(), s)
	require.ErrorIs(t, err, ErrNotReady)
}
