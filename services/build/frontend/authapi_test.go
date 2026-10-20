package frontend

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

type fakeAuthAPI struct {
	procedure string
	header    http.Header
	body      string
	err       error
}

func (f *fakeAuthAPI) Call(_ context.Context, procedure string, h http.Header, body []byte) (int, http.Header, []byte, error) {
	f.procedure, f.header, f.body = procedure, h, string(body)
	if f.err != nil {
		return 0, nil, nil, f.err
	}
	return http.StatusOK, http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"x=y"}}, []byte(`{"ok":true}`), nil
}

func authCall(procedure, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, procedure, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Connect-Protocol-Version", "1")
	r.Header.Set("Authorization", "Bearer tau-token")
	r.Header.Set("Cookie", "session=1")
	r.Header.Set(TauTokenHeader, "other")
	return r
}

func TestAuthCallsAreCarriedUnchanged(t *testing.T) {
	a := &fakeAuthAPI{}
	h := newHarness(t, Config{}, WithAuthAPI(a))
	w := h.do(t, authCall(authv2connect.SessionServiceLoginProcedure, `{"name":"root"}`))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, `{"ok":true}`, w.Body.String())
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
	require.Empty(t, w.Header().Get("Set-Cookie"), "only the headers a Connect answer needs come back")

	require.Equal(t, authv2connect.SessionServiceLoginProcedure, a.procedure)
	require.Equal(t, `{"name":"root"}`, a.body)
	require.Equal(t, http.Header{
		"Content-Type":             {"application/json"},
		"Connect-Protocol-Version": {"1"},
		"Authorization":            {"Bearer tau-token"},
	}, a.header)
}

func TestOnlyTheNamedAuthCallsPass(t *testing.T) {
	a := &fakeAuthAPI{}
	h := newHarness(t, Config{}, WithAuthAPI(a))

	// Admission is what nodes ask of auth, never a caller.
	w := h.do(t, authCall(authv2connect.IdentityServiceAdmitProcedure, `{}`))
	require.Equal(t, http.StatusNotFound, w.Code)
	w = h.do(t, authCall(authv2connect.IdentityServiceVerifyProcedure, `{}`))
	require.Equal(t, http.StatusNotFound, w.Code)

	r := httptest.NewRequest(http.MethodGet, authv2connect.SessionServiceProvidersProcedure, nil)
	require.Equal(t, http.StatusMethodNotAllowed, h.do(t, r).Code)

	w = h.do(t, authCall(authv2connect.SessionServiceLoginProcedure, strings.Repeat("x", maxAuthBody+1)))
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	a.err = errors.New("no auth")
	w = h.do(t, authCall(authv2connect.SessionServiceLoginProcedure, `{}`))
	require.Equal(t, http.StatusBadGateway, w.Code)

	// Without an auth API, none of the routes exists.
	none := newHarness(t, Config{})
	require.Equal(t, http.StatusNotFound, none.do(t, authCall(authv2connect.SessionServiceLoginProcedure, `{}`)).Code)
	require.Error(t, WithAuthAPI(nil)(&options{}))
}

func TestAuthCallsAreRateLimitedPerAddress(t *testing.T) {
	a := &fakeAuthAPI{}
	h := newHarness(t, Config{CallerRate: Rate{Burst: 2, Per: time.Hour}}, WithAuthAPI(a))
	for range 2 {
		require.Equal(t, http.StatusOK, h.do(t, authCall(authv2connect.SessionServiceLoginProcedure, `{}`)).Code)
	}
	require.Equal(t, http.StatusTooManyRequests, h.do(t, authCall(authv2connect.SessionServiceLoginProcedure, `{}`)).Code)
	// Another address has its own bucket.
	r := authCall(authv2connect.SessionServiceLoginProcedure, `{}`)
	r.RemoteAddr = "198.51.100.7:4000"
	require.Equal(t, http.StatusOK, h.do(t, r).Code)
	r.RemoteAddr = "garbage"
	require.Equal(t, "garbage", clientAddr(r))
}
