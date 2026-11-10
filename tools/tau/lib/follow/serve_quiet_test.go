package follow

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// Nothing changing for serveQuiet ends Serve with ExitQuiet, not 0: a rollout
// that never propagates must not look green in CI.
func TestServeReturnsWhenNothingChanged(t *testing.T) {
	defer func(q, h time.Duration) { serveQuiet, heartbeat = q, h }(serveQuiet, heartbeat)
	serveQuiet, heartbeat = 300*time.Millisecond, 50*time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "same") }))
	defer srv.Close()
	targets := []Target{{Resource: "function fn1", Node: "n1", URL: srv.URL}}
	_, before, _ := Baseline(targets)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var progress bytes.Buffer
	served, code := Serve(ctx, targets, func(t Target) string { return before[t] }, 10*time.Millisecond, time.Now(), &progress)
	assert.Equal(t, code, ExitQuiet)
	assert.Equal(t, served[0].Note, "not changed by this push")
	assert.Assert(t, strings.Contains(progress.String(), "have not picked it up"), progress.String())
	assert.Assert(t, strings.Contains(progress.String(), "still waiting for 1 of 1 nodes"), progress.String())
}

// A new website answers 404 before its first build: that is its baseline.
func TestBaselineKeepsWebsite404(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	keep, _, skipped := Baseline([]Target{
		{Resource: "website site13", Node: "n1", URL: srv.URL},
		{Resource: "function kv", Node: "n1", URL: srv.URL},
	})
	assert.Equal(t, len(keep), 1)
	assert.Equal(t, keep[0].Resource, "website site13")
	assert.Equal(t, len(skipped), 1)
}
