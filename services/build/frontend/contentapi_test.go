package frontend

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

func readCall(t *testing.T, h *harness, body string) (int, readResponse, string) {
	t.Helper()
	w := h.do(t, jsonCall(http.MethodPost, "/forges/github.com/read", body))
	raw := bodyOf(t, w)
	var got readResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal([]byte(raw), &got))
	}
	return w.Code, got, raw
}

func commitCall(t *testing.T, h *harness, body string) (int, commitResponse, string) {
	t.Helper()
	w := h.do(t, jsonCall(http.MethodPost, "/forges/github.com/commit", body))
	raw := bodyOf(t, w)
	var got commitResponse
	if w.Code == http.StatusCreated {
		require.NoError(t, json.Unmarshal([]byte(raw), &got))
	}
	return w.Code, got, raw
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// A file, a directory and the root, at the default branch, a branch, a tag
// and a full ref name — each read by fetching that tip alone.
func TestRead(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	head := repos.host.seed(t, "acme/pay.git", map[string]string{"config/config.yaml": "id: abc\n", "README": "hi"})

	code, got, raw := readCall(t, h, `{"repository":"github.com/acme/pay","path":"config/config.yaml"}`)
	require.Equal(t, http.StatusOK, code, raw)
	require.Equal(t, readResponse{Repository: "github.com/acme/pay", Ref: "refs/heads/main", Commit: head, Path: "config/config.yaml", Type: "file", Content: []byte("id: abc\n")}, got)

	code, got, _ = readCall(t, h, `{"repository":"github.com/acme/pay"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "tree", got.Type)
	require.Equal(t, []treeEntry{{Name: "README", Type: "file"}, {Name: "config", Type: "tree"}}, got.Entries)

	code, got, _ = readCall(t, h, `{"repository":"github.com/acme/pay","path":"/config/"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []treeEntry{{Name: "config.yaml", Type: "file"}}, got.Entries)

	for _, ref := range []string{"main", "refs/heads/main", "v1"} {
		code, got, raw = readCall(t, h, `{"repository":"github.com/acme/pay","ref":"`+ref+`","path":"README"}`)
		require.Equal(t, http.StatusOK, code, raw)
		require.Equal(t, head, got.Commit, ref)
		require.Equal(t, "hi", string(got.Content))
	}

	for body, want := range map[string]int{
		`nope`: http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","path":"../x"}`:      http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","path":".git/HEAD"}`: http.StatusBadRequest,
		`{"repository":"gitlab.com/acme/pay"}`:                    http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","path":"missing"}`:   http.StatusNotFound,
		`{"repository":"github.com/acme/pay","ref":"nope"}`:       http.StatusNotFound,
		`{"repository":"github.com/acme/absent"}`:                 http.StatusBadGateway,
	} {
		code, _, raw := readCall(t, h, body)
		require.Equal(t, want, code, body+": "+raw)
	}
}

// Reading takes read access, asked with the caller's connection; a
// repository they cannot read is the same 404 as one that is not there.
func TestReadIsAuthorized(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	repos.host.seed(t, "acme/pay.git", map[string]string{"README": "hi"})
	h.connections.forge = map[string]string{"github.com": testToken}

	repos.read = AccessDenied
	code, _, _ := readCall(t, h, `{"repository":"github.com/acme/pay"}`)
	require.Equal(t, http.StatusNotFound, code)
	repos.read = AccessUnauthenticated
	code, _, _ = readCall(t, h, `{"repository":"github.com/acme/pay"}`)
	require.Equal(t, http.StatusUnauthorized, code)
	repos.read = AccessGranted
	repos.err = ErrUnsupported
	code, _, _ = readCall(t, h, `{"repository":"github.com/acme/pay"}`)
	require.Equal(t, http.StatusNotImplemented, code)
}

func TestReadAnEmptyRepositoryOrATooLargeOne(t *testing.T) {
	h, repos := repoHarness(t, Config{MaxFetchBytes: 64})
	repos.host.bare(t, "acme/empty.git")
	repos.host.seed(t, "acme/pay.git", map[string]string{"big": strings.Repeat("x", 4096)})
	code, _, raw := readCall(t, h, `{"repository":"github.com/acme/empty"}`)
	require.Equal(t, http.StatusNotFound, code)
	require.Contains(t, raw, "empty")
	code, _, raw = readCall(t, h, `{"repository":"github.com/acme/pay"}`)
	require.Equal(t, http.StatusRequestEntityTooLarge, code, raw)
}

// The first commit of a new repository, then one on top of it: each lands
// on the branch, authored as the connection's account, and git agrees.
func TestCommit(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	repos.host.bare(t, "acme/new.git")

	code, first, raw := commitCall(t, h, `{"repository":"github.com/acme/new","message":"init","files":[{"path":"config/config.yaml","content":"`+b64("id: abc\n")+`"}]}`)
	require.Equal(t, http.StatusCreated, code, raw)
	require.Equal(t, "main", first.Branch)
	require.Equal(t, "id: abc\n", repos.host.show(t, "acme/new.git", "main:config/config.yaml")+"\n")
	log := git(t, repos.host.root+"/acme/new.git", "log", "--format=%an <%ae>|%s", "main")
	require.Equal(t, "alice <alice@example.com>|init", log)

	code, second, raw := commitCall(t, h, `{"repository":"github.com/acme/new","parent":"`+first.Commit+`","message":"more","files":[{"path":"config/b.yaml","content":"`+b64("b")+`"},{"path":"top","content":"`+b64("t")+`"}]}`)
	require.Equal(t, http.StatusCreated, code, raw)
	require.Equal(t, second.Commit, git(t, repos.host.root+"/acme/new.git", "rev-parse", "main"))
	require.Equal(t, first.Commit, git(t, repos.host.root+"/acme/new.git", "rev-parse", "main^"))
	require.Equal(t, "b", repos.host.show(t, "acme/new.git", "main:config/b.yaml"))
	git(t, repos.host.root+"/acme/new.git", "fsck", "--strict")

	// Deleting the last file of a directory removes the directory.
	code, _, raw = commitCall(t, h, `{"repository":"github.com/acme/new","message":"rm","files":[{"path":"config/b.yaml","delete":true},{"path":"config/config.yaml","delete":true}]}`)
	require.Equal(t, http.StatusCreated, code, raw)
	require.Equal(t, "top", git(t, repos.host.root+"/acme/new.git", "ls-tree", "--name-only", "main"))

	// A commit built on a tip that has since moved is refused, not merged.
	code, _, raw = commitCall(t, h, `{"repository":"github.com/acme/new","parent":"`+first.Commit+`","message":"late","files":[{"path":"x","content":""}]}`)
	require.Equal(t, http.StatusConflict, code, raw)

	// A new branch starts from the default one.
	code, got, raw := commitCall(t, h, `{"repository":"github.com/acme/new","branch":"feature","message":"f","files":[{"path":"f","content":"`+b64("f")+`"}]}`)
	require.Equal(t, http.StatusCreated, code, raw)
	require.Equal(t, "feature", got.Branch)
	require.Equal(t, "f\ntop", git(t, repos.host.root+"/acme/new.git", "ls-tree", "--name-only", "feature"))

	// The same files again commit nothing.
	code, _, raw = commitCall(t, h, `{"repository":"github.com/acme/new","message":"same","files":[{"path":"top","content":"`+b64("t")+`"}]}`)
	require.Equal(t, http.StatusConflict, code, raw)
}

// The first commit of an empty repository may name its branch, and an empty
// repository holding nothing still gets its empty tree.
func TestCommitToAnEmptyRepository(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	repos.host.bare(t, "acme/new.git")
	code, _, raw := commitCall(t, h, `{"repository":"github.com/acme/new","branch":"trunk","message":"nothing","files":[{"path":"gone","delete":true}]}`)
	require.Equal(t, http.StatusCreated, code, raw)
	require.Equal(t, "", git(t, repos.host.root+"/acme/new.git", "ls-tree", "trunk"))
}

func TestCommitRefusals(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	repos.host.seed(t, "acme/pay.git", map[string]string{"dir/file": "x", "file": "y"})
	for body, want := range map[string]int{
		`nope`: http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","files":[{"path":"a"}]}`:                                                          http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","message":"m"}`:                                                                   http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","message":"m","branch":"a..b","files":[{"path":"a"}]}`:                            http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","message":"m","parent":"abc","files":[{"path":"a"}]}`:                             http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","message":"m","files":[{"path":"a"},{"path":"/a/"}]}`:                             http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","message":"m","files":[{"path":""}]}`:                                             http.StatusBadRequest,
		`{"repository":"gitlab.com/acme/pay","message":"m","files":[{"path":"a"}]}`:                                            http.StatusBadRequest,
		`{"repository":"github.com/acme/pay","message":"m","files":[{"path":"dir","content":""}]}`:                             http.StatusConflict,
		`{"repository":"github.com/acme/pay","message":"m","files":[{"path":"file/under","content":""}]}`:                      http.StatusConflict,
		`{"repository":"github.com/acme/pay","message":"m","files":[{"path":"a"}],"parent":"` + strings.Repeat("0", 40) + `"}`: http.StatusConflict,
	} {
		code, _, raw := commitCall(t, h, body)
		require.Equal(t, want, code, body+": "+raw)
	}
	tooMany := `{"repository":"github.com/acme/pay","message":"m","files":[` + strings.Repeat(`{"path":"a"},`, maxCommitFiles) + `{"path":"b"}]}`
	code, _, _ := commitCall(t, h, tooMany)
	require.Equal(t, http.StatusBadRequest, code)

	huge := `{"repository":"github.com/acme/pay","message":"m","files":[{"path":"a","content":"` + strings.Repeat("A", maxCommitBody) + `"}]}`
	code, _, _ = commitCall(t, h, huge)
	require.Equal(t, http.StatusRequestEntityTooLarge, code)
}

// Committing takes write access; read is not enough.
func TestCommitTakesWrite(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	repos.host.bare(t, "acme/new.git")
	h.identity.write = func(string, repokey.Key) (Access, error) { return AccessDenied, nil }
	code, _, _ := commitCall(t, h, `{"repository":"github.com/acme/new","message":"m","files":[{"path":"a"}]}`)
	require.Equal(t, http.StatusNotFound, code)
	require.Zero(t, repos.host.asked(), "the forge was never asked")
}

// A symlink, an executable kept executable, and a submodule, which has no
// content here.
func TestReadSpecialEntries(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	bare := repos.host.bare(t, "acme/odd.git")
	work := t.TempDir()
	git(t, work, "init", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "run"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.Symlink("run", filepath.Join(work, "link")))
	git(t, work, "add", "-A")
	git(t, work, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("a", 40)+",sub")
	git(t, work, "commit", "-m", "odd")
	git(t, work, "push", bare, "main")

	code, got, raw := readCall(t, h, `{"repository":"github.com/acme/odd"}`)
	require.Equal(t, http.StatusOK, code, raw)
	require.Equal(t, []treeEntry{{Name: "link", Type: "symlink"}, {Name: "run", Type: "file"}, {Name: "sub", Type: "submodule"}}, got.Entries)
	code, got, _ = readCall(t, h, `{"repository":"github.com/acme/odd","path":"sub"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "submodule", got.Type)
	require.Empty(t, got.Content)
	code, got, _ = readCall(t, h, `{"repository":"github.com/acme/odd","path":"link"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "run", string(got.Content))

	code, _, raw = commitCall(t, h, `{"repository":"github.com/acme/odd","message":"m","files":[{"path":"run","content":"`+b64("#!/bin/sh\nexit 0\n")+`"}]}`)
	require.Equal(t, http.StatusCreated, code, raw)
	require.Contains(t, git(t, bare, "ls-tree", "main", "run"), "100755")
}
