package frontend

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

type fakeInstaller struct {
	inspected []string // forge token|key|callback
	installed []Install
	tokens    []string
	inspect   Inspection
	err       error
}

func (f *fakeInstaller) Inspect(_ context.Context, token string, key repokey.Key, callback *url.URL) (Inspection, error) {
	f.inspected = append(f.inspected, token+"|"+key.String()+"|"+callback.String())
	if f.err != nil {
		return Inspection{}, f.err
	}
	return f.inspect, nil
}

func (f *fakeInstaller) Install(_ context.Context, token string, _ repokey.Key, in Install) error {
	f.tokens = append(f.tokens, token)
	if f.err != nil {
		return f.err
	}
	f.installed = append(f.installed, in)
	return nil
}

func linkCall(path, body string) *http.Request {
	r := authed(http.MethodPost, path, testToken)
	r.Body = io.NopCloser(strings.NewReader(body))
	return r
}

const goodKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample tau"

func TestLinkRoutesNeedAnInstaller(t *testing.T) {
	h := newHarness(t, Config{})
	for _, p := range []string{"/link/" + testHookID + "/inspect", "/link/" + testHookID + "/install"} {
		w := h.do(t, linkCall(p, `{}`))
		require.Equal(t, http.StatusNotFound, w.Code, p)
	}
	_, err := New(Config{}, WithInstaller(nil))
	require.Error(t, err)
}

// Inspecting reads the repository under the caller's connection, opened here,
// and says where this cloud's hook for the project delivers.
func TestLinkInspect(t *testing.T) {
	inst := &fakeInstaller{inspect: Inspection{Repository: "github.com/acme/Pay", Admin: true, Hooked: true}}
	h := newHarness(t, Config{PublicURL: "http://example.com"}, WithInstaller(inst))
	h.connections.forge = map[string]string{"github.com": testToken}

	w := h.do(t, linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`))
	require.Equal(t, http.StatusOK, w.Code)
	var got inspectResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, inspectResponse{Repository: "github.com/acme/Pay", Admin: true, Hooked: true, Callback: "http://example.com/hook/" + testHookID}, got)
	require.Equal(t, []string{testToken + "|" + testRepoKey + "|http://example.com/hook/" + testHookID}, inst.inspected)

	for body, want := range map[string]int{
		`nope`:                     http.StatusBadRequest,
		`{"repository":"Not/Key"}`: http.StatusBadRequest,
	} {
		require.Equal(t, want, h.do(t, linkCall("/link/"+testHookID+"/inspect", body)).Code, body)
	}

	// A caller who may not write to it is denied, as on every route.
	r := linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`)
	r.Header.Set("Authorization", bearerPrefix+"someone-else")
	h.connections.forge = nil
	require.Equal(t, http.StatusNotFound, h.do(t, r).Code)
	h.connections.forge = map[string]string{"github.com": testToken}

	inst.err = fmt.Errorf("%w: no hooks here", ErrCannotInstall)
	w = h.do(t, linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, bodyOf(t, w), "no hooks here")
}

// The callback is the frontend's name under the cloud, and a configured public
// address overrides it.
func TestLinkCallbackIsDerivedFromTheCloud(t *testing.T) {
	inst := &fakeInstaller{inspect: Inspection{Repository: testRepoKey, Admin: true}}
	h := newHarness(t, Config{}, WithInstaller(inst), WithCloudFqdn("Tau.Example.io."))
	h.connections.forge = map[string]string{"github.com": testToken}
	r := linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`)
	r.Host = "attacker.example"
	require.Equal(t, http.StatusOK, h.do(t, r).Code)
	require.Contains(t, inst.inspected[0], "|https://build-frontend.tau.example.io/hook/"+testHookID)

	h = newHarness(t, Config{PublicURL: "https://ci.operator.example"}, WithInstaller(inst), WithCloudFqdn("tau.example.io"))
	h.connections.forge = map[string]string{"github.com": testToken}
	require.Equal(t, http.StatusOK, h.do(t, linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`)).Code)
	require.Contains(t, inst.inspected[1], "|https://ci.operator.example/hook/"+testHookID)
}

// The callback is the configured public address when there is no cloud.
func TestLinkCallback(t *testing.T) {
	inst := &fakeInstaller{inspect: Inspection{Repository: testRepoKey, Admin: true}}
	h := newHarness(t, Config{PublicURL: "https://build.example.com/"}, WithInstaller(inst))
	h.connections.forge = map[string]string{"github.com": testToken}
	require.Equal(t, http.StatusOK, h.do(t, linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`)).Code)
	require.Contains(t, inst.inspected[0], "|https://build.example.com/hook/"+testHookID)

	// UNSET, NOTHING IS INSTALLED: the Host header is the caller's to name,
	// and a hook aimed by it would deliver wherever the caller said.
	h = newHarness(t, Config{}, WithInstaller(inst))
	h.connections.forge = map[string]string{"github.com": testToken}
	r := linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`)
	r.Host = "attacker.example"
	r.TLS = &tls.ConnectionState{}
	w := h.do(t, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	body := bodyOf(t, w)
	require.Contains(t, body, "publicURL")
	require.Contains(t, body, "cloudFqdn")
	require.Equal(t, http.StatusServiceUnavailable, h.do(t, linkCall("/link/"+testHookID+"/install", `{"repository":"`+testRepoKey+`","deploy_key":"`+goodKey+`"}`)).Code)
	require.Len(t, inst.inspected, 1)

	h = newHarness(t, Config{PublicURL: "ftp://build.example.com"}, WithInstaller(inst))
	require.Equal(t, http.StatusInternalServerError, h.do(t, linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`)).Code)

	h = newHarness(t, Config{PublicURL: "::"}, WithInstaller(inst))
	require.Equal(t, http.StatusInternalServerError, h.do(t, linkCall("/link/"+testHookID+"/inspect", `{"repository":"`+testRepoKey+`"}`)).Code)
	require.Equal(t, http.StatusInternalServerError, h.do(t, linkCall("/link/"+testHookID+"/install", `{"repository":"`+testRepoKey+`","deploy_key":"`+goodKey+`"}`)).Code)
}

// Installing puts the project's STORED secret on a repository the project is
// made of, delivering only to this cloud; the caller sends a public key.
func TestLinkInstall(t *testing.T) {
	inst := &fakeInstaller{}
	h := newHarness(t, Config{PublicURL: "http://example.com"}, WithInstaller(inst))
	h.connections.forge = map[string]string{"github.com": testToken}
	path := "/link/" + testHookID + "/install"

	w := h.do(t, linkCall(path, `{"repository":"`+testRepoKey+`","deploy_key":"`+goodKey+`","replace":true}`))
	require.Equal(t, http.StatusNoContent, w.Code, bodyOf(t, w))
	require.Len(t, inst.installed, 1)
	in := inst.installed[0]
	require.Equal(t, "http://example.com/hook/"+testHookID, in.Callback.String())
	require.Equal(t, testSecret, string(in.Secret))
	require.Equal(t, "tau http://example.com", in.Title)
	require.Equal(t, goodKey, in.DeployKey)
	require.True(t, in.Replace)
	require.Equal(t, []string{testToken}, inst.tokens, "the forge credential is opened here")

	for body, want := range map[string]int{
		`nope`: http.StatusBadRequest,
		`{"repository":"Not/Key","deploy_key":"` + goodKey + `"}`:               http.StatusBadRequest,
		`{"repository":"` + testRepoKey + `","deploy_key":"rsa"}`:               http.StatusBadRequest,
		`{"repository":"` + testRepoKey + `","deploy_key":"ssh-a\nssh-b"}`:      http.StatusBadRequest,
		`{"repository":"github.com/acme/other","deploy_key":"` + goodKey + `"}`: http.StatusBadRequest,
	} {
		require.Equal(t, want, h.do(t, linkCall(path, body)).Code, body)
	}

	// A project with no stored secret is linked first.
	w = h.do(t, linkCall("/link/proj-unknown/install", `{"repository":"`+testRepoKey+`","deploy_key":"`+goodKey+`"}`))
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, bodyOf(t, w), "link it first")

	// One the caller may not write to is denied.
	r := linkCall(path, `{"repository":"`+testRepoKey+`","deploy_key":"`+goodKey+`"}`)
	r.Header.Set("Authorization", bearerPrefix+"someone-else")
	h.connections.forge = nil
	require.Equal(t, http.StatusNotFound, h.do(t, r).Code)
	h.connections.forge = map[string]string{"github.com": testToken}

	for err, want := range map[error]int{
		fmt.Errorf("%w: holds write", ErrNotAdmin): http.StatusForbidden,
		ErrAlreadyHooked:         http.StatusConflict,
		errors.New("forge down"): http.StatusBadGateway,
	} {
		inst.err = err
		require.Equal(t, want, h.do(t, linkCall(path, `{"repository":"`+testRepoKey+`","deploy_key":"`+goodKey+`"}`)).Code, err.Error())
	}

	// The register cannot be asked.
	inst.err = nil
	h.hooks.get = func(string) (Hook, error) { return Hook{}, errors.New("register down") }
	require.Equal(t, http.StatusBadGateway, h.do(t, linkCall(path, `{"repository":"`+testRepoKey+`","deploy_key":"`+goodKey+`"}`)).Code)
}

// A connection that will not open stops both routes before the forge.
func TestLinkRoutesNeedTheConnection(t *testing.T) {
	inst := &fakeInstaller{}
	h := newHarness(t, Config{PublicURL: "http://example.com"}, WithInstaller(inst))
	h.connections.err = map[string]error{"github.com": &NotConnectedError{Reason: "not connected to github.com: connect it first"}}
	for _, p := range []string{"/link/" + testHookID + "/inspect", "/link/" + testHookID + "/install"} {
		w := h.do(t, linkCall(p, `{"repository":"`+testRepoKey+`","deploy_key":"`+goodKey+`"}`))
		require.Equal(t, http.StatusUnauthorized, w.Code, p)
	}
	require.Empty(t, inst.inspected)
	require.Empty(t, inst.tokens)
}
