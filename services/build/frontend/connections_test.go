package frontend

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// A renewed tau token comes back in its header, and a host is opened once
// per request however many times the request asks.
func TestARenewedTokenComesBack(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(newLinker(t)))
	h.connections.renewed = "tau-renewed"
	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "tau-renewed", w.Header().Get(RenewedTokenHeader))
	require.Equal(t, 1, h.connections.opened)

	w = h.do(t, authed(http.MethodGet, "/forges/github.com/projects", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "tau-renewed", w.Header().Get(RenewedTokenHeader))
}

func TestANotConnectedHostIsAReasonToConnect(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(newLinker(t)))
	h.connections.err = map[string]error{"github.com": &NotConnectedError{Reason: "not connected to github.com: connect it first"}}
	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, bodyOf(t, w), "connect it first")

	w = h.do(t, authed(http.MethodGet, "/forges/github.com/projects", testToken))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestATokenThatDoesNotOpenIsUnauthorized(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(newLinker(t)))
	h.connections.err = map[string]error{"github.com": ErrBadCredential}
	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	w = h.do(t, authed(http.MethodGet, "/forges/github.com/projects", testToken))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAConnectionThatCannotBeOpenedIsNotADenial(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(newLinker(t)))
	h.connections.err = map[string]error{"github.com": errors.New("auth unreachable")}
	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, testToken))
	require.Equal(t, http.StatusBadGateway, w.Code)
	w = h.do(t, authed(http.MethodGet, "/forges/github.com/projects", testToken))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

// A project on two forges is reached with one token: the connection for each
// repository's host is the one asked.
func TestAProjectOnTwoForges(t *testing.T) {
	h := newHarness(t, Config{}, WithLinker(newLinker(t)))
	h.projects.repositories = func(string) ([]string, error) {
		return []string{"gitlab.com/acme/config", testRepoKey}, nil
	}
	h.connections.forge = map[string]string{"gitlab.com": "glpat", "github.com": "gho"}
	var asked []string
	h.identity.write = func(token string, key repokey.Key) (Access, error) {
		asked = append(asked, key.Host+"="+token)
		if token == "gho" {
			return AccessGranted, nil
		}
		return AccessDenied, nil
	}
	w := h.do(t, authed(http.MethodGet, "/jobs/"+testProject, "tau"))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"gitlab.com=glpat", "github.com=gho"}, asked)

	// Not connected to one of them: the other still grants.
	h.connections.err = map[string]error{"gitlab.com": &NotConnectedError{Reason: "connect gitlab.com"}}
	asked = nil
	w = h.do(t, authed(http.MethodGet, "/jobs/"+testProject, "tau"))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []string{"github.com=gho"}, asked)

	// And when the other denies, connecting is what is said.
	h.connections.forge["github.com"] = "nope"
	w = h.do(t, authed(http.MethodGet, "/jobs/"+testProject, "tau"))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, bodyOf(t, w), "connect gitlab.com")
}

func TestCORSExposesTheRenewedToken(t *testing.T) {
	h := newHarness(t, Config{Origins: []string{"https://console.example"}})
	r := authed(http.MethodGet, "/jobs/"+testProject, testToken)
	r.Header.Set("Origin", "https://console.example")
	w := h.do(t, r)
	require.Contains(t, w.Header().Get("Access-Control-Expose-Headers"), RenewedTokenHeader)
}

func TestANilConnectionsIsRefused(t *testing.T) {
	require.Error(t, WithConnections(nil)(&options{}))
	_, err := New(Config{}, WithHooks(&fakeHooks{}), WithDeliveries(&fakeDeliveries{}), WithProjects(&fakeProjects{}), WithBackend(&fakeBackend{}), WithIdentity(&fakeIdentity{}))
	require.ErrorContains(t, err, "connections")
}
