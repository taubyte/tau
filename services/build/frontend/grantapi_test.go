package frontend

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/register"
)

func grantReq(token, project, body string) *http.Request {
	return withBody(authed(http.MethodPost, "/projects/"+project+"/grants", token), []byte(body))
}

func grantsOf(t *testing.T, h *harness, token, project string) []grantResponse {
	t.Helper()
	w := h.do(t, authed(http.MethodGet, "/projects/"+project+"/grants", token))
	require.Equal(t, http.StatusOK, w.Code)
	var out []grantResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

func TestAnOwnerSharesAProject(t *testing.T) {
	h, _ := registered(t)
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor("tok-alice", "QmPay", linkBody)).Code)
	require.Empty(t, grantsOf(t, h, "tok-alice", "QmPay"))

	// bob reads nothing until granted
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/QmPay", "tok-bob")).Code)

	w := h.do(t, grantReq("tok-alice", "QmPay", `{"user":"bob","role":"viewer"}`))
	require.Equal(t, http.StatusCreated, w.Code)
	var made map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &made))
	require.NotEmpty(t, made["id"])

	require.Equal(t, http.StatusOK, h.do(t, authed(http.MethodGet, "/projects/QmPay", "tok-bob")).Code)
	require.Equal(t, []string{"QmPay"}, projectsOf(t, h, "tok-bob"))
	got := grantsOf(t, h, "tok-alice", "QmPay")
	require.Len(t, got, 1)
	require.Equal(t, grantResponse{ID: made["id"], User: "bob", Role: "viewer", By: "alice", At: got[0].At}, got[0])

	// a viewer may not share it on
	require.Equal(t, http.StatusNotFound, h.do(t, grantReq("tok-bob", "QmPay", `{"user":"eve","role":"owner"}`)).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/QmPay/grants", "tok-bob")).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodDelete, "/projects/QmPay/grants/"+made["id"], "tok-bob")).Code)

	// revoked, bob is out again, and the revocation is final
	require.Equal(t, http.StatusNoContent, h.do(t, authed(http.MethodDelete, "/projects/QmPay/grants/"+made["id"], "tok-alice")).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodGet, "/projects/QmPay", "tok-bob")).Code)
	require.Empty(t, projectsOf(t, h, "tok-bob"))
	require.Empty(t, grantsOf(t, h, "tok-alice", "QmPay"))

	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodDelete, "/projects/QmPay/grants/NOPE", "tok-alice")).Code)
}

func TestGrantRefusals(t *testing.T) {
	h, l := registered(t)
	require.Equal(t, http.StatusCreated, h.do(t, linkRequestFor("tok-alice", "QmPay", linkBody)).Code)

	for name, body := range map[string]string{
		"not json": "{",
		"no user":  `{"role":"viewer"}`,
		"no role":  `{"user":"bob","role":"admin"}`,
	} {
		require.Equal(t, http.StatusBadRequest, h.do(t, grantReq("tok-alice", "QmPay", body)).Code, name)
	}
	require.Equal(t, http.StatusUnauthorized, h.do(t, grantReq("nope", "QmPay", `{"user":"bob","role":"viewer"}`)).Code)
	require.Equal(t, http.StatusNotFound, h.do(t, grantReq("tok-alice", "QmNone", `{"user":"bob","role":"viewer"}`)).Code)

	l.fail = errors.New("register down")
	require.Equal(t, http.StatusBadGateway, h.do(t, grantReq("tok-alice", "QmPay", `{"user":"bob","role":"viewer"}`)).Code)
}

func TestTheRegisterFailingUnderAnOwnerIsSaid(t *testing.T) {
	l := linkedTestProject(t)
	broken := &linker{Register: l.Register, fail: errors.New("down")}
	h := newHarness(t, Config{}, WithLinker(broken))
	require.Equal(t, http.StatusBadGateway, h.do(t, authed(http.MethodGet, "/projects/"+testProject+"/grants", testToken)).Code)
	require.Equal(t, http.StatusBadGateway, h.do(t, grantReq(testToken, testProject, `{"user":"bob","role":"viewer"}`)).Code)
	require.Equal(t, http.StatusBadGateway, h.do(t, authed(http.MethodDelete, "/projects/"+testProject+"/grants/x", testToken)).Code)

	long := make([]byte, maxIDLength+1)
	for i := range long {
		long[i] = 'a'
	}
	require.Equal(t, http.StatusNotFound, h.do(t, authed(http.MethodDelete, "/projects/"+testProject+"/grants/"+string(long), testToken)).Code)
	require.True(t, register.RoleOwner.Includes(register.RoleViewer))
}
