package frontend

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewRefusesEveryMissingDependency(t *testing.T) {
	hooks, deliveries, projects, backend, callers := &fakeHooks{}, &fakeDeliveries{}, &fakeProjects{}, &fakeBackend{}, &fakeCallers{}

	type testCase struct {
		name string
		call func() (*Service, error)
		want string
	}
	for _, tc := range []testCase{
		// Omitted entirely: the option was never passed.
		{"no hooks", func() (*Service, error) {
			return New(Config{}, WithDeliveries(deliveries), WithProjects(projects), WithBackend(backend), WithCallers(callers))
		}, "hooks"},
		{"no deliveries", func() (*Service, error) {
			return New(Config{}, WithHooks(hooks), WithProjects(projects), WithBackend(backend), WithCallers(callers))
		}, "verify a webhook delivery"},
		{"no projects", func() (*Service, error) {
			return New(Config{}, WithHooks(hooks), WithDeliveries(deliveries), WithBackend(backend), WithCallers(callers))
		}, "projects"},
		{"no backend", func() (*Service, error) {
			return New(Config{}, WithHooks(hooks), WithDeliveries(deliveries), WithProjects(projects), WithCallers(callers))
		}, "backend"},
		{"no callers", func() (*Service, error) {
			return New(Config{}, WithHooks(hooks), WithDeliveries(deliveries), WithProjects(projects), WithBackend(backend))
		}, "verify a caller"},

		// Passed as nil: the option itself refuses, so a bad argument fails
		// where it was written rather than at the end of New.
		{"a nil hooks", func() (*Service, error) { return New(Config{}, WithHooks(nil)) }, "hooks"},
		{"a nil deliveries", func() (*Service, error) { return New(Config{}, WithDeliveries(nil)) }, "verify a webhook delivery"},
		{"a nil projects", func() (*Service, error) { return New(Config{}, WithProjects(nil)) }, "projects"},
		{"a nil backend", func() (*Service, error) { return New(Config{}, WithBackend(nil)) }, "backend"},
		{"nil callers", func() (*Service, error) { return New(Config{}, WithCallers(nil)) }, "verify a caller"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.call()
			require.Nil(t, s)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestNewRefusesAnUnusableConfig(t *testing.T) {
	type testCase struct {
		name string
		cfg  Config
		want string
	}
	for _, tc := range []testCase{
		{"negative body cap", Config{MaxBodyBytes: -1}, "maxBodyBytes"},
		{"negative header timeout", Config{ReadHeaderTimeout: -time.Second}, "readHeaderTimeout"},
		{"negative idle timeout", Config{IdleTimeout: -time.Second}, "idleTimeout"},
		{"negative shutdown timeout", Config{ShutdownTimeout: -time.Second}, "shutdownTimeout"},
		{"negative backend timeout", Config{BackendTimeout: -time.Second}, "backendTimeout"},
		{"negative follow backoff", Config{FollowBackoff: -time.Second}, "followBackoff"},
		{"negative follow backoff ceiling", Config{FollowBackoffMax: -time.Second}, "followBackoffMax"},
		{"negative cache ttl", Config{SecretCacheTTL: -time.Second}, "secretCacheTTL"},
		{"negative cache size", Config{SecretCacheMax: -1}, "secretCacheMax"},
		{"negative burst", Config{HookRate: Rate{Burst: -1, Per: time.Second}}, "hookRate"},
		{"negative period", Config{WebhookRate: Rate{Burst: 1, Per: -time.Second}}, "webhookRate"},
		{"negative caller burst", Config{CallerRate: Rate{Burst: -1, Per: time.Second}}, "callerRate"},
		{"negative job period", Config{JobRate: Rate{Burst: 1, Per: -time.Second}}, "jobRate"},
		{"a rate with no period", Config{HookRate: Rate{Burst: 5}}, "no period"},
		{"a caller rate with no period", Config{CallerRate: Rate{Burst: 5}}, "no period"},
		{"a job rate with no period", Config{JobRate: Rate{Burst: 5}}, "no period"},
		{"an empty eligible ref", Config{EligibleRefs: []string{"refs/heads/main", ""}}, "empty ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(tc.cfg, WithHooks(&fakeHooks{}), WithProjects(&fakeProjects{}), WithBackend(&fakeBackend{}), WithCallers(&fakeCallers{}))
			require.Nil(t, s)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestNewRefusesANilOption(t *testing.T) {
	type testCase struct {
		name string
		opt  Option
	}
	for _, tc := range []testCase{
		{"clock", WithClock(nil)},
		{"logger", WithLogger(nil)},
		{"http server", WithHTTPServer(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(Config{}, WithHooks(&fakeHooks{}), WithProjects(&fakeProjects{}), WithBackend(&fakeBackend{}), WithCallers(&fakeCallers{}), tc.opt)
			require.Nil(t, s)
			require.ErrorContains(t, err, "option")
		})
	}
}

func TestAnOptionsOwnErrorSurvives(t *testing.T) {
	sentinel := errors.New("no")
	s, err := New(Config{}, WithHooks(&fakeHooks{}), WithProjects(&fakeProjects{}), WithBackend(&fakeBackend{}), WithCallers(&fakeCallers{}),
		func(*options) error { return sentinel })
	require.Nil(t, s)
	require.ErrorIs(t, err, sentinel)
}

func TestNewBindsNothing(t *testing.T) {
	h := newHarness(t, Config{Listen: "127.0.0.1:0"})

	require.NotNil(t, h.Handler())
	require.Nil(t, h.Addr())
}

func TestStartServesAndCloseStops(t *testing.T) {
	h := newHarness(t, Config{Listen: "127.0.0.1:0"})

	require.NoError(t, h.Start(t.Context()))
	addr := h.Addr()
	require.NotNil(t, addr)

	// A real request on a kernel-chosen port.
	resp, err := http.Get(fmt.Sprintf("http://%s/job/%s", addr, testJobID))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	require.NoError(t, h.Close())
	require.NoError(t, h.Close(), "Close is idempotent")

	_, err = net.DialTimeout("tcp", addr.String(), time.Second)
	require.Error(t, err, "the listener is gone")
}

func TestStartIsRefusedTwiceAndAfterClose(t *testing.T) {
	h := newHarness(t, Config{Listen: "127.0.0.1:0"})

	require.NoError(t, h.Start(t.Context()))
	require.ErrorContains(t, h.Start(t.Context()), "already started")
	require.NoError(t, h.Close())
	require.ErrorContains(t, h.Start(t.Context()), "closed")
}

func TestStartRefusesToInventAnAddress(t *testing.T) {
	h := newHarness(t, Config{})
	require.ErrorContains(t, h.Start(t.Context()), "no address")
}

func TestStartReportsAnUnusableAddress(t *testing.T) {
	h := newHarness(t, Config{Listen: "256.256.256.256:1"})
	require.ErrorContains(t, h.Start(t.Context()), "listen on")
}

func TestCloseBeforeStartIsFine(t *testing.T) {
	h := newHarness(t, Config{})
	require.NoError(t, h.Close())
}

func TestTheHTTPServerHookRunsBeforeItServes(t *testing.T) {
	var seen *http.Server
	h := newHarness(t, Config{Listen: "127.0.0.1:0", ReadHeaderTimeout: 3 * time.Second},
		WithHTTPServer(func(s *http.Server) { seen = s; s.ReadTimeout = 7 * time.Second }))

	require.NoError(t, h.Start(t.Context()))
	t.Cleanup(func() { h.Close() })

	require.NotNil(t, seen)
	require.Equal(t, 3*time.Second, seen.ReadHeaderTimeout)
	require.Equal(t, 7*time.Second, seen.ReadTimeout)
	require.Equal(t, h.Handler(), seen.Handler)
}

func TestServiceNeedsOnlyFakes(t *testing.T) {
	s, err := New(Config{}, WithHooks(&fakeHooks{}), WithDeliveries(&fakeDeliveries{}), WithProjects(&fakeProjects{}), WithBackend(&fakeBackend{}), WithCallers(&fakeCallers{}),
		WithLinker(linkedTestProject(t)), WithVault(&fakeVault{}))
	require.NoError(t, err)
	require.Nil(t, s.Addr())

	for _, r := range guardedRoutes() {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, authed(r.method, r.path, testToken))
		require.Less(t, w.Code, 300, r.name)
	}
}

func TestEveryCallOutOfThisProcessCarriesADeadline(t *testing.T) {
	h := newHarness(t, Config{BackendTimeout: 25 * time.Millisecond})

	h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))

	deadline, ok := h.backend.lastContext().Deadline()
	require.True(t, ok, "the backend was called with no deadline")
	require.LessOrEqual(t, time.Until(deadline), 25*time.Millisecond)

	// And the webhook path too, which is the one a stranger reaches.
	h.do(t, webhookRequest(webhookOpts{}))
	deadline, ok = h.backend.lastContext().Deadline()
	require.True(t, ok)
	require.LessOrEqual(t, time.Until(deadline), 25*time.Millisecond)
}

func TestAServerThatDiesSaysSoInTheLog(t *testing.T) {
	h := newHarness(t, Config{Listen: "127.0.0.1:0"})
	require.NoError(t, h.Start(t.Context()))
	t.Cleanup(func() { h.Close() })

	h.startMu.Lock()
	listener, serving := h.listener, h.serving
	h.startMu.Unlock()

	require.NoError(t, listener.Close())
	<-serving

	require.Contains(t, h.logs.joined(), "serving on")
}

func TestCloseStopsEvenWhenARequestWillNot(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	entered := make(chan struct{})

	h := newHarness(t, Config{Listen: "127.0.0.1:0", ShutdownTimeout: time.Millisecond},
		WithHTTPServer(func(s *http.Server) {
			s.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				close(entered)
				<-release
			})
		}))

	require.NoError(t, h.Start(t.Context()))
	addr := h.Addr().String()

	go func() {
		resp, err := http.Get("http://" + addr + "/anything")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered

	// Shutdown gives up and Close takes the listener regardless; the error says
	// which of the two happened.
	require.Error(t, h.Close())
	require.Error(t, h.Close(), "and the same error every time")

	_, err := net.DialTimeout("tcp", addr, time.Second)
	require.Error(t, err)
}
