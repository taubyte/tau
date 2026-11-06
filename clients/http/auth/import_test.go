package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	httpClient "github.com/taubyte/tau/clients/http"
)

// Import registers the project under the id its config carries, through the
// import route, so a cloud files the project's builds where the config does.
func TestProjectImportSendsConfigID(t *testing.T) {
	var path string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Write([]byte(`{"project":{"id":"QmConfigID","name":"shop"}}`))
	}))
	defer srv.Close()

	c, err := New(context.Background(), httpClient.URL(srv.URL), httpClient.Auth("token"), httpClient.Provider(httpClient.Github), httpClient.Unsecure())
	if err != nil {
		t.Fatal(err)
	}

	p := &Project{Name: "shop"}
	if err := p.Import(c, "QmConfigID", "11", "22"); err != nil {
		t.Fatal(err)
	}

	if path != "/project/import/shop" {
		t.Fatalf("posted to %q", path)
	}
	if body["project-id"] != "QmConfigID" {
		t.Fatalf("project-id = %v", body["project-id"])
	}
	cfg, _ := body["config"].(map[string]any)
	code, _ := body["code"].(map[string]any)
	if cfg["id"] != "11" || code["id"] != "22" {
		t.Fatalf("repositories = %v, %v", cfg, code)
	}
}
