package frontend

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCORSOnlyForConfiguredOrigins(t *testing.T) {
	h := newHarness(t, Config{Origins: []string{"https://console.example"}})

	pre := httptest.NewRequest(http.MethodOptions, "/job/"+testJobID, nil)
	pre.Header.Set("Origin", "https://console.example")
	pre.Header.Set("Access-Control-Request-Method", "GET")
	w := h.do(t, pre)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, "https://console.example", w.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Authorization")
	require.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))

	r := authed(http.MethodGet, "/job/"+testJobID, testToken)
	r.Header.Set("Origin", "https://console.example")
	w = h.do(t, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "https://console.example", w.Header().Get("Access-Control-Allow-Origin"))

	// Any other origin gets nothing, and a preflight from it is not answered.
	pre.Header.Set("Origin", "https://evil.example")
	w = h.do(t, pre)
	require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
	require.NotEqual(t, http.StatusNoContent, w.Code)
}
