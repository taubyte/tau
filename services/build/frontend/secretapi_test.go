package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
)

// fakeVault records what reached it, so a route that authorized wrongly is
// visible as a write that happened rather than only as a status code.
type fakeVault struct {
	mu     sync.Mutex
	values map[string][]byte
	err    error
}

func newVault() *fakeVault { return &fakeVault{values: map[string][]byte{}} }

func vaultKey(project, environment, name string) string {
	return project + "/" + environment + "/" + name
}

func (v *fakeVault) Set(_ context.Context, project, environment, name string, value []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return v.err
	}
	v.values[vaultKey(project, environment, name)] = value
	return nil
}

func (v *fakeVault) Delete(_ context.Context, project, environment, name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return v.err
	}
	delete(v.values, vaultKey(project, environment, name))
	return nil
}

func (v *fakeVault) List(_ context.Context, project, environment string) ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.err != nil {
		return nil, v.err
	}

	var names []string
	prefix := project + "/" + environment + "/"
	for k := range v.values {
		if name, ok := strings.CutPrefix(k, prefix); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

func (v *fakeVault) held() map[string][]byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := map[string][]byte{}
	for k, val := range v.values {
		out[k] = val
	}
	return out
}

func withBody(r *http.Request, body []byte) *http.Request {
	r.Body = http.NoBody
	if body != nil {
		r.Body = httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body)).Body
	}
	return r
}

const secretPath = "/secrets/" + testProject + "/production"

func TestASecretIsSetListedAndDeleted(t *testing.T) {
	v := newVault()
	h := newHarness(t, Config{}, WithVault(v))

	w := h.do(t, withBody(authed(http.MethodPut, secretPath+"/DATABASE_URL", testToken), []byte("postgres://x")))
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, []byte("postgres://x"),
		v.held()[vaultKey(testProject, "production", "DATABASE_URL")])

	w = h.do(t, authed(http.MethodGet, secretPath, testToken))
	require.Equal(t, http.StatusOK, w.Code)
	var names []string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &names))
	require.Equal(t, []string{"DATABASE_URL"}, names)

	w = h.do(t, authed(http.MethodDelete, secretPath+"/DATABASE_URL", testToken))
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Empty(t, v.held())
}

func TestRotatingASecretNeedsNoPush(t *testing.T) {
	v := newVault()
	h := newHarness(t, Config{}, WithVault(v))

	for _, value := range []string{"old", "new"} {
		w := h.do(t, withBody(authed(http.MethodPut, secretPath+"/TOKEN", testToken), []byte(value)))
		require.Equal(t, http.StatusNoContent, w.Code)
	}

	require.Equal(t, []byte("new"), v.held()[vaultKey(testProject, "production", "TOKEN")])
	require.Empty(t, h.backend.sent, "nothing was queued")
}

func TestAnEnvironmentWithNothingSetIsAnEmptyList(t *testing.T) {
	h := newHarness(t, Config{}, WithVault(newVault()))

	w := h.do(t, authed(http.MethodGet, secretPath, testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `[]`, bodyOf(t, w))
}

func TestThereIsNoRouteThatReadsASecretBack(t *testing.T) {
	v := newVault()
	h := newHarness(t, Config{}, WithVault(v))
	require.NoError(t, v.Set(t.Context(), testProject, "production", "STRIPE", []byte("sk_live_dontleakme")))

	w := h.do(t, authed(http.MethodGet, secretPath+"/STRIPE", testToken))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code,
		"the path exists for PUT and DELETE; a GET of it must not be a read")

	// And the listing, which does exist, carries no value.
	w = h.do(t, authed(http.MethodGet, secretPath, testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, bodyOf(t, w), "sk_live")
}

func TestOnlySomebodyWhoMayPushMaySetASecret(t *testing.T) {
	v := newVault()
	h := newHarness(t, Config{}, WithVault(v))
	h.projects.role = func(string, string) (register.Role, error) { return register.RoleNone, nil }

	for _, r := range []*http.Request{
		authed(http.MethodGet, secretPath, testToken),
		withBody(authed(http.MethodPut, secretPath+"/TOKEN", testToken), []byte("v")),
		authed(http.MethodDelete, secretPath+"/TOKEN", testToken),
	} {
		w := h.do(t, r)
		require.Equal(t, http.StatusNotFound, w.Code, "%s %s", r.Method, r.URL.Path)
	}
	require.Empty(t, v.held(), "a denied caller wrote nothing")
}

func TestASecretRouteWithNoCredentialIsUnauthorized(t *testing.T) {
	h := newHarness(t, Config{}, WithVault(newVault()))

	w := h.do(t, authed(http.MethodGet, secretPath, ""))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestACloudWithNoVaultRefusesLikeADenial(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, withBody(authed(http.MethodPut, secretPath+"/TOKEN", testToken), []byte("v")))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestAnOversizedValueIsRefusedAndNotStored(t *testing.T) {
	v := newVault()
	h := newHarness(t, Config{}, WithVault(v))

	w := h.do(t, withBody(authed(http.MethodPut, secretPath+"/BIG", testToken),
		bytes.Repeat([]byte("a"), maxSecretValue+1)))
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	require.Empty(t, v.held())

	// And exactly at the cap it is accepted, so the limit is the limit rather
	// than approximately it.
	w = h.do(t, withBody(authed(http.MethodPut, secretPath+"/BIG", testToken),
		bytes.Repeat([]byte("a"), maxSecretValue)))
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestALongPathSegmentIsRefused(t *testing.T) {
	v := newVault()
	h := newHarness(t, Config{}, WithVault(v))

	long := strings.Repeat("n", maxIDLength+1)

	// A long environment, on the route that has one.
	w := h.do(t, authed(http.MethodGet, "/secrets/"+testProject+"/"+long, testToken))
	require.Equal(t, http.StatusNotFound, w.Code)

	// A long secret name, on the routes that have one.
	w = h.do(t, withBody(authed(http.MethodPut, secretPath+"/"+long, testToken), []byte("v")))
	require.Equal(t, http.StatusNotFound, w.Code)

	w = h.do(t, authed(http.MethodDelete, secretPath+"/"+long, testToken))
	require.Equal(t, http.StatusNotFound, w.Code)

	require.Empty(t, v.held())
}

func TestAnEnvironmentCannotCarryASlash(t *testing.T) {
	v := newVault()
	h := newHarness(t, Config{}, WithVault(v))

	w := h.do(t, authed(http.MethodGet, "/secrets/"+testProject+"/a/b", testToken))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)

	// And a fourth segment matches nothing at all.
	w = h.do(t, withBody(authed(http.MethodPut, "/secrets/"+testProject+"/prod/a/b", testToken), []byte("v")))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, v.held())
}

func TestAVaultFailureIsAnUpstreamFailure(t *testing.T) {
	v := newVault()
	v.err = connect.NewError(connect.CodeUnavailable, errors.New("the swarm is unreachable"))
	h := newHarness(t, Config{}, WithVault(v))

	w := h.do(t, withBody(authed(http.MethodPut, secretPath+"/TOKEN", testToken), []byte("v")))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.NotContains(t, bodyOf(t, w), "swarm", "the upstream's own words do not cross")
}

func TestAVaultMustNotBeNil(t *testing.T) {
	_, err := New(Config{}, WithHooks(&fakeHooks{}), WithProjects(&fakeProjects{}),
		WithBackend(&fakeBackend{}), WithCallers(&fakeCallers{}), WithVault(nil))
	require.ErrorContains(t, err, "no vault")
}

func TestSettingASecretIsBelievedAtOnceByTheHookItVerifies(t *testing.T) {
	h := newHarness(t, Config{}, WithVault(newVault()))
	h.hooks.get = func(id string) (Hook, error) {
		return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(testSecret)}}}, nil
	}
	push := func() { h.do(t, webhookRequest(webhookOpts{hookID: testProject})) }

	push()
	push()
	require.Equal(t, 1, h.hooks.count(), "a hook is cached")

	require.Equal(t, http.StatusNoContent, h.do(t, withBody(authed(http.MethodPut, secretPath+"/TOKEN", testToken), []byte("v"))).Code)
	push()
	require.Equal(t, 2, h.hooks.count(), "setting a secret drops the cached hook")

	require.Equal(t, http.StatusNoContent, h.do(t, authed(http.MethodDelete, secretPath+"/TOKEN", testToken)).Code)
	push()
	require.Equal(t, 3, h.hooks.count(), "deleting one does too")
}
