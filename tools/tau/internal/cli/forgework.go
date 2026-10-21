package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// forgeWork is what `tau link` does at the forges, one session per host under
// the profile's own credential for it.
type forgeWork struct {
	forges   *forge.Registry
	creds    forgeCreds
	sessions map[string]*forge.Session
	tokens   map[string]forge.Credential
}

// repoState is a repository as the forge answered for it.
type repoState struct {
	key           repokey.Key
	defaultBranch string
	created       bool
	// hooked is whether this cloud's webhook for the project is already there.
	hooked bool
}

func (w *forgeWork) session(ctx context.Context, host string) (*forge.Session, error) {
	if s, ok := w.sessions[host]; ok {
		return s, nil
	}
	cred, err := w.creds.credential(ctx, host)
	if err != nil {
		return nil, err
	}
	s, err := w.forges.Host(host, cred)
	if err != nil {
		return nil, err
	}
	if w.tokens == nil {
		w.tokens = map[string]forge.Credential{}
	}
	w.sessions[host], w.tokens[host] = s, cred
	return s, nil
}

// inspect reads k at its forge — creating it first when asked and it is not
// there — and checks the credential may install on it.
func (w *forgeWork) inspect(ctx context.Context, k repokey.Key, callback *url.URL, create, private bool) (repoState, error) {
	s, err := w.session(ctx, k.Host)
	if err != nil {
		return repoState{}, err
	}
	var st repoState
	repo, err := s.Repository(ctx, k)
	var missing *forge.NotFoundError
	switch {
	case errors.As(err, &missing) && create:
		if repo, err = s.Create(ctx, k, forge.CreateOptions{Private: private}); err != nil {
			return repoState{}, fmt.Errorf("creating %s: %w", k, err)
		}
		st.created = true
	case err != nil:
		return repoState{}, fmt.Errorf("reading %s on its forge: %w", k, err)
	}
	// THE FORGE'S SPELLING, because a delivery names the repository the way
	// the forge does and the cloud compares it exactly.
	st.key, st.defaultBranch = repo.Key, repo.DefaultBranch

	// ASKED BEFORE ANYTHING IS REGISTERED: installing a webhook and a deploy
	// key takes admin on most forges, and a link that registered the id and
	// then could not install would leave a project nobody can push to.
	access, err := s.Access(ctx, st.key)
	if err != nil {
		return repoState{}, fmt.Errorf("reading your access to %s: %w", st.key, err)
	}
	if !access.CanAdmin() {
		return repoState{}, fmt.Errorf("your %s credential may not administer %s, and installing a webhook and a deploy key takes admin (or run with --manual)", k.Host, st.key)
	}
	hooks, err := s.ListPushHooks(ctx, st.key)
	if err != nil {
		return repoState{}, fmt.Errorf("listing %s's webhooks: %w", st.key, err)
	}
	for _, h := range hooks {
		if h.Callback == callback.String() {
			st.hooked = true
		}
	}
	return st, nil
}

// install puts c's webhook and read-only deploy key on its repository,
// removing this cloud's earlier ones first when c replaces them.
func (w *forgeWork) install(ctx context.Context, c repoCreds, callback *url.URL) error {
	s, err := w.session(ctx, c.repository.Host)
	if err != nil {
		return err
	}
	if c.replace {
		hooks, err := s.ListPushHooks(ctx, c.repository)
		if err != nil {
			return err
		}
		for _, h := range hooks {
			if h.Callback == callback.String() {
				if err := s.RemovePushHook(ctx, c.repository, h.Handle); err != nil {
					return err
				}
			}
		}
		keys, err := s.ListDeployKeys(ctx, c.repository)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if k.Title == c.title {
				if err := s.RemoveDeployKey(ctx, c.repository, k.Handle); err != nil {
					return err
				}
			}
		}
	}
	if _, err := s.RegisterPushHook(ctx, c.repository, callback, c.hook); err != nil {
		return err
	}
	_, err = s.InstallDeployKey(ctx, c.repository, c.title, c.authz)
	return err
}

// push pushes dir's HEAD to st's default branch over https with the forge
// credential, and returns the commit pushed. A directory that is not a git
// repository yet becomes one, with everything in it as the first commit and
// the repository as its origin; one that is, is pushed as it stands.
func (w *forgeWork) push(ctx context.Context, dir string, st repoState) (string, error) {
	s, err := w.session(ctx, st.key.Host)
	if err != nil {
		return "", err
	}
	addr, err := s.CloneAddress(st.key, forge.HTTPS)
	if err != nil {
		return "", err
	}
	branch := st.defaultBranch
	if branch == "" {
		branch = "main"
	}
	g := gitIn{dir: dir, auth: basicAuth(addr.HTTPUsername, string(w.tokens[st.key.Host].Expose()))}
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		for _, args := range [][]string{
			{"init", "-q", "-b", branch},
			{"add", "-A"},
			{"commit", "-q", "-m", "tau project"},
			{"remote", "add", "origin", addr.URL},
		} {
			if _, err := g.run(ctx, args...); err != nil {
				return "", err
			}
		}
	}
	if _, err := g.run(ctx, "push", "-q", addr.URL, "HEAD:refs/heads/"+branch); err != nil {
		return "", err
	}
	head, err := g.run(ctx, "rev-parse", "HEAD")
	return strings.TrimSpace(head), err
}

func basicAuth(user, token string) string {
	if user == "" {
		user = "x-access-token"
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token))
}

// gitIn runs git in a directory. The credential travels in the environment,
// never in the arguments or the remote URL, so it lands in no process list,
// .git/config or error message.
type gitIn struct {
	dir, auth string
}

func (g gitIn) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: "+g.auth,
		"GIT_CONFIG_KEY_1=credential.helper", "GIT_CONFIG_VALUE_1=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
