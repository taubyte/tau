package dream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/v3/assert"
)

// PushAll runs the pushAll fixture, as `dream inject push-all` does.
func TestPushAllInjects(t *testing.T) {
	var path string
	var body struct{ Params []string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	assert.NilError(t, PushAll(context.Background(), srv.URL, "blackhole", "QmProj", "main"))
	assert.Equal(t, path, "POST /fixture/blackhole/pushAll")
	assert.DeepEqual(t, body.Params, []string{"QmProj", "main"})

	srv.Close()
	assert.Assert(t, PushAll(context.Background(), srv.URL, "blackhole", "QmProj", "main") != nil)
}
