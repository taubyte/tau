package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-github/v71/github"
)

// keyServer is a repository's deploy keys on a fake GitHub.
type keyServer struct {
	mu      sync.Mutex
	keys    map[int64][2]string // id -> title, key
	deleted []int64
	failDel bool
}

func (k *keyServer) handler(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/keys"):
		var parts []string
		for id, tk := range k.keys {
			parts = append(parts, fmt.Sprintf(`{"id":%d,"title":%q,"key":%q}`, id, tk[0], tk[1]))
		}
		w.Write([]byte("[" + strings.Join(parts, ",") + "]"))
	case r.Method == http.MethodDelete:
		if k.failDel {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var id int64
		fmt.Sscanf(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "%d", &id)
		delete(k.keys, id)
		k.deleted = append(k.deleted, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func keyClient(t *testing.T, k *keyServer) *githubClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(k.handler))
	t.Cleanup(srv.Close)
	gh := github.NewClient(nil)
	gh.BaseURL, _ = url.Parse(srv.URL + "/")
	owner, name := "me", "tb_project"
	return &githubClient{Client: gh, ctx: context.Background(), current_repository: &github.Repository{
		Name: &name, Owner: &github.User{Login: &owner},
	}}
}

func TestRemoveOtherDeployKeys(t *testing.T) {
	k := &keyServer{keys: map[int64][2]string{
		1: {"taubyte_deploy_key", "ecdsa-sha2-nistp256 OLD1"},
		2: {"taubyte_deploy_key", "ecdsa-sha2-nistp256 OLD2"},
		3: {"taubyte_deploy_key", "ecdsa-sha2-nistp256 NEW"},
		4: {"someone_elses_key", "ssh-ed25519 THEIRS"},
	}}
	client := keyClient(t, k)

	// the new key as Auth holds it: an authorized_keys line, newline included
	if err := client.RemoveOtherDeployKeys("taubyte_deploy_key", "ecdsa-sha2-nistp256 NEW\n"); err != nil {
		t.Fatal(err)
	}
	if _, ok := k.keys[3]; !ok {
		t.Fatal("removed the key just added")
	}
	if _, ok := k.keys[4]; !ok {
		t.Fatal("removed a key Taubyte did not add")
	}
	if len(k.keys) != 2 || len(k.deleted) != 2 {
		t.Fatalf("left %v, deleted %v", k.keys, k.deleted)
	}
}

func TestRemoveOtherDeployKeysReportsFailures(t *testing.T) {
	k := &keyServer{failDel: true, keys: map[int64][2]string{
		1: {"taubyte_deploy_key", "ecdsa-sha2-nistp256 OLD"},
		2: {"taubyte_deploy_key", "ecdsa-sha2-nistp256 NEW"},
	}}
	if err := keyClient(t, k).RemoveOtherDeployKeys("taubyte_deploy_key", "ecdsa-sha2-nistp256 NEW"); err == nil {
		t.Fatal("a key GitHub would not delete was not reported")
	}
}

func TestSameDeployKey(t *testing.T) {
	if sameDeployKey("ecdsa-sha2-nistp256 AAAA comment\n") != sameDeployKey("ecdsa-sha2-nistp256 AAAA") {
		t.Fatal("the comment made two lines of one key differ")
	}
}
