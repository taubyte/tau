package project

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	httpClient "github.com/taubyte/tau/clients/http"
	authClient "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/tools/tau/i18n/printer"
)

// An id the cloud holds for another config repository belongs to another
// project: a fork or a copy must get a new one, not overwrite it.
func TestImportableID(t *testing.T) {
	defer printer.SetOutput(printer.Noop())()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/projects/taken":
			w.Write([]byte(`{"project":{"id":"taken","name":"p","Repositories":{"configuration":{"fullname":"orig/tb_p"}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	auth, err := authClient.New(context.Background(), httpClient.URL(srv.URL), httpClient.Auth("t"), httpClient.Provider(httpClient.Github))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ id, config, want string }{
		{"", "me/tb_p", ""},
		{"fresh", "me/tb_p", "fresh"},
		{"taken", "orig/tb_p", "taken"},
		{"taken", "fork/tb_p", ""},
	} {
		if got := importableID(auth, c.id, c.config); got != c.want {
			t.Errorf("importableID(%q, %q) = %q; want %q", c.id, c.config, got, c.want)
		}
	}
}
