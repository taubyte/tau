package frontend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/services/build/livelog"
)

type fakeAssets map[string]string

func (f fakeAssets) Read(_ context.Context, cid string) (io.ReadCloser, error) {
	v, ok := f[cid]
	if !ok {
		return nil, errors.New("no such content")
	}
	return io.NopCloser(strings.NewReader(v)), nil
}

// lzwOf is a log as the backend stores it.
func lzwOf(t *testing.T, log string) string {
	t.Helper()
	stored, err := io.ReadAll(livelog.Compressed(strings.NewReader(log)))
	require.NoError(t, err)
	return string(stored)
}

func TestJobAssets(t *testing.T) {
	h := newHarness(t, Config{}, WithAssets(fakeAssets{"bafylog": lzwOf(t, "built ok\n"), "bafyasset": "\x00wasm"}))

	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/logs/2026-01-01T00:00:00Z", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "built ok\n", bodyOf(t, w))
	require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))

	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/assets/resource-1", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "\x00wasm", bodyOf(t, w))

	for path, want := range map[string]int{
		"/job/" + testJobID + "/assets/bafyasset": http.StatusNotFound,
		"/job/" + testJobID + "/logs/other":       http.StatusNotFound,
		"/job/nope/assets/resource-1":             http.StatusNotFound,
	} {
		require.Equal(t, want, h.do(t, authed(http.MethodGet, path, testToken)).Code, path)
	}
	require.Equal(t, http.StatusUnauthorized, h.do(t, authed(http.MethodGet, "/job/"+testJobID+"/assets/resource-1", "stranger")).Code)

	bad := newHarness(t, Config{}, WithAssets(fakeAssets{}))
	require.Equal(t, http.StatusBadGateway, bad.do(t, authed(http.MethodGet, "/job/"+testJobID+"/assets/resource-1", testToken)).Code)

	none := newHarness(t, Config{})
	require.Equal(t, http.StatusServiceUnavailable, none.do(t, authed(http.MethodGet, "/job/"+testJobID+"/assets/resource-1", testToken)).Code)
	_, err := New(Config{}, WithAssets(nil))
	require.Error(t, err)
}

func TestCloud(t *testing.T) {
	h := newHarness(t, Config{}, WithCloudFqdn("tau.example.com"))
	w := h.do(t, authed(http.MethodGet, "/cloud", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"fqdn":"tau.example.com"}`, bodyOf(t, w))
	require.Equal(t, http.StatusUnauthorized, h.do(t, authed(http.MethodGet, "/cloud", "")).Code)
	require.Equal(t, http.StatusServiceUnavailable, newHarness(t, Config{}).do(t, authed(http.MethodGet, "/cloud", testToken)).Code)
	limited := newHarness(t, Config{CallerRate: Rate{Burst: 1, Per: 1e12}}, WithCloudFqdn("x"))
	limited.do(t, authed(http.MethodGet, "/cloud", testToken))
	require.Equal(t, http.StatusTooManyRequests, limited.do(t, authed(http.MethodGet, "/cloud", testToken)).Code)
}
