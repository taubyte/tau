package repositoryLib

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taubyte/tau/pkg/git"
	"gotest.tools/v3/assert"
)

// tau talks to GitHub through go-git with the stored token: no git process,
// so no credential helper (Git Credential Manager) and no prompt can run.
func TestGitUsesStoredTokenNoHelper(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := git.New(context.Background(), git.URL(srv.URL+"/me/tb_code_x.git"), git.Root(t.TempDir()+"/x"), git.Token("tok123"))
	assert.Assert(t, err != nil)
	assert.Equal(t, auth, "Basic "+base64.StdEncoding.EncodeToString([]byte("taubyte:tok123")))
}
