package follow

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// Only the resources the push changed are waited on; the rest are named.
func TestServeStopsWhenChangedResourceServes(t *testing.T) {
	var v atomic.Value
	v.Store("v1")
	hello := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, v.Load().(string)) }))
	defer hello.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "same") }))
	defer other.Close()
	targets := []Target{
		{Resource: "function hello", Node: "n1", URL: hello.URL},
		{Resource: "function hello", Node: "n2", URL: hello.URL},
		{Resource: "function libfn", Node: "n1", URL: other.URL},
	}
	_, before, _ := Baseline(targets)
	time.AfterFunc(50*time.Millisecond, func() { v.Store("v2") })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	served, code := Serve(ctx, targets, func(t Target) string { return before[t] }, poll, time.Now(), nil)
	assert.Equal(t, code, 0)
	assert.Assert(t, served[0].Changed && served[1].Changed)
	assert.Equal(t, served[2].Note, "not changed by this push")
}

// The probe uses the function's method; 404 or no answer is left out, once per resource.
func TestBaselineMethodAndUnfingerprintable(t *testing.T) {
	post := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer post.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()

	keep, before, skipped := Baseline([]Target{
		{Resource: "function big", Node: "n1", URL: post.URL, Method: "POST"},
		{Resource: "function kv", Node: "n1", URL: post.URL},
		{Resource: "function kv", Node: "n2", URL: dead.URL},
		{Resource: "function st", Node: "n1", URL: dead.URL},
	})
	assert.Equal(t, len(keep), 1)
	assert.Equal(t, keep[0].Resource, "function big")
	assert.Assert(t, before[keep[0]] != "")
	assert.Equal(t, len(skipped), 2, "%v", skipped)
}
