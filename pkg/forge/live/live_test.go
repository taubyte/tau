//go:build docker_integration

package live_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/containers/backends/docker"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/plm/deploy"
)

// instance is one booted forge, set up the way the scenario needs it: an admin
// account that owns a namespace and two repositories, and an ordinary user the
// scenario acts as. It is the whole of what differs between forges; the
// scenario below is the same walk for every one of them.
type instance struct {
	name string

	// host is the repokey host the forge's clone URLs name, and reg binds the
	// driver under test to it with the strict policy — loosened only to accept
	// a cleartext callback, because the receiver is this test's loopback
	// listener.
	host string
	reg  *forge.Registry

	// user is the credential the scenario acts as. It is NOT an administrator:
	// an admin is answered "admin" about every repository on the instance, and
	// the access ladder would prove nothing.
	user      forge.Credential
	userLogin string
	// pushAs is how a commit is pushed over HTTPS as that user.
	pushAs transport.AuthMethod

	// adminLogin owns namespace, which the user is not a member of, and the
	// two repositories the user is asked about: one public, one private.
	adminLogin string
	namespace  string
	public     repokey.Key
	private    repokey.Key

	// hostKeys are the ssh host keys the forge answers a clone with.
	hostKeys []gossh.PublicKey

	// listen is where the hook receiver listens, and reach is the address the
	// forge is told to deliver to — the same on a host-networked container,
	// the bridge gateway on one that is not.
	listen, reach string

	// branchURL is the admin API URL that answers 200 for a branch of a
	// repository and 404 for none, with the header that authenticates it. It
	// is how a refused push is shown to have written nothing, rather than
	// trusting whatever error the client printed.
	branchURL func(key repokey.Key, branch string) string
	asAdmin   http.Header

	// noMembership and noHooks name the faces this forge's driver lacks, declared
	// rather than read off the session so a driver that loses a face fails the walk;
	// walk asserts each is refused as *forge.UnsupportedError.
	noMembership, noHooks bool
}

// walk is everything tau asks of a forge, against a real one, in the order a
// link and a build ask it. Membership and hooks are optional faces: a forge
// declares the ones it lacks (see instance), and walk then asserts each is
// refused by name instead of answered.
func walk(t *testing.T, in *instance) {
	ctx := t.Context()

	sess, err := in.reg.Host(in.host, in.user)
	require.NoError(t, err)
	require.NoError(t, sess.Require(
		forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapDeployKeys, forge.CapCloneAddr))
	var unsupported *forge.UnsupportedError
	if in.noMembership {
		require.ErrorAs(t, sess.Require(forge.CapMembership), &unsupported)
	} else {
		require.NoError(t, sess.Require(forge.CapMembership))
	}
	if in.noHooks {
		require.ErrorAs(t, sess.Require(forge.CapPushHooks), &unsupported)
		require.ErrorAs(t, sess.Require(forge.CapSignatures), &unsupported)
	} else {
		require.NoError(t, sess.Require(forge.CapPushHooks, forge.CapSignatures))
	}

	t.Log("identity")
	who, err := sess.Whoami(ctx)
	require.NoError(t, err)
	require.Equal(t, in.userLogin, who.Login)
	require.NotEmpty(t, who.Subject)

	bad, err := in.reg.Host(in.host, forge.Token("not-a-token-this-forge-issued"))
	require.NoError(t, err)
	_, err = bad.Whoami(ctx)
	var credErr *forge.CredentialError
	require.ErrorAs(t, err, &credErr, "a token the forge never issued must read as a refused credential, not an outage")

	t.Log("access")
	require.Equal(t, forge.AccessRead, access(t, in, in.public),
		"a public repository the user holds no grant on is readable and nothing more")
	_, err = forSession(t, in, in.private).Access(ctx, in.private)
	var missing *forge.NotFoundError
	require.ErrorAs(t, err, &missing, "a private repository the user cannot see is indistinguishable from one that does not exist")

	if in.noMembership {
		t.Log("membership is refused, not answered")
		isMember, err := in.reg.IsActiveMember(ctx, in.host, in.namespace, in.adminLogin)
		require.ErrorAs(t, err, &unsupported)
		require.False(t, isMember)
	} else {
		t.Log("membership")
		isMember, err := in.reg.IsActiveMember(ctx, in.host, in.namespace, in.adminLogin)
		require.NoError(t, err)
		require.True(t, isMember, "%s owns %s", in.adminLogin, in.namespace)
		isMember, err = in.reg.IsActiveMember(ctx, in.host, in.namespace, in.userLogin)
		require.NoError(t, err, "a non-member is a definitive (false, nil), never an error")
		require.False(t, isMember)
	}

	t.Log("create")
	name := fmt.Sprintf("live-%d", time.Now().UnixNano())
	key := keyOf(t, in, in.userLogin+"/"+name)
	s := forSession(t, in, key)
	created, err := s.Create(ctx, key, forge.CreateOptions{Description: "tau live harness", Private: true})
	require.NoError(t, err)
	require.Equal(t, key, created.Key, "the key is derived from the forge's answer, and the forge made what was asked")
	require.True(t, created.Private)
	t.Cleanup(func() {
		// Deleted at the end of the walk; this is for a walk that failed first.
		s.Delete(context.Background(), key)
	})
	require.Equal(t, forge.AccessAdmin, access(t, in, key), "the creator owns what it created")

	t.Log("list")
	listed := false
	require.NoError(t, s.ListRepositories(ctx, func(r forge.Repository) bool {
		listed = listed || r.Key == key
		return !listed
	}))
	require.True(t, listed, "%s was created by this credential and its listing does not name it", key)

	t.Log("deploy key")
	private, public := gittest.KeyPair(t)
	authorized := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(public)))
	dk, err := s.InstallDeployKey(ctx, key, "tau-live", authorized)
	require.NoError(t, err)
	keys, err := s.ListDeployKeys(ctx, key)
	require.NoError(t, err)
	require.True(t, holdsKey(keys, dk, public), "the deploy key just installed is not among %v", keys)

	var hook forge.Hook
	var rx *receiver
	if in.noHooks {
		t.Log("hooks are refused, not registered")
		callback, err := url.Parse("http://" + in.reach + "/never")
		require.NoError(t, err)
		secret, err := forge.NewSecret()
		require.NoError(t, err)
		_, err = s.RegisterPushHook(ctx, key, callback, secret)
		require.ErrorAs(t, err, &unsupported)
		_, err = s.ListPushHooks(ctx, key)
		require.ErrorAs(t, err, &unsupported)
	} else {
		t.Log("hook")
		rx = receive(t, in.listen)
		callback, err := url.Parse("http://" + in.reach + rx.path)
		require.NoError(t, err)
		secret, err := forge.NewSecret()
		require.NoError(t, err)
		hook, err = s.RegisterPushHook(ctx, key, callback, secret)
		require.NoError(t, err)
		hooks, err := s.ListPushHooks(ctx, key)
		require.NoError(t, err)
		require.Contains(t, hooks, forge.InstalledPushHook{Handle: hook.Handle, Callback: callback.String()})
	}

	t.Log("push")
	pushed := push(t, created.HTTPS, in.pushAs)

	if !in.noHooks {
		t.Log("delivery")
		verified := rx.await(t, func(d forge.Delivery) bool {
			p, err := in.reg.VerifyPush(hook, d)
			if errors.Is(err, forge.ErrNotAPush) {
				return false
			}
			require.NoError(t, err, "a delivery %s signed and sent failed verification", in.name)
			require.Equal(t, forge.Push{Key: key, Ref: "refs/heads/main", Commit: pushed}, p)

			tampered := forge.Delivery{Header: d.Header, Body: bytes.Replace(d.Body, []byte(pushed), []byte(strings.Repeat("e", len(pushed))), 1)}
			_, err = in.reg.VerifyPush(hook, tampered)
			require.Error(t, err, "the same delivery naming another commit verified")
			return true
		})
		require.True(t, verified, "%s never delivered the push", in.name)
	}

	t.Log("clone with the deploy key")
	repo, err := s.Repository(ctx, key)
	require.NoError(t, err)
	address := sshAddress(t, repo.SSH)
	clone := deploy.NewClone(deployKey(private),
		// THE ADDRESS IS THE FORGE'S, NOT THE DRIVER'S OFFLINE GUESS. This
		// forge's ssh and http listen on different ports of one host, and
		// CloneAddress can only derive the ssh one from the key's host — which
		// carries the HTTP port. Repository() is the forge naming its own ssh
		// address, which is the documented answer on a ported host.
		deploy.WithAddress(fixed(address)),
		deploy.WithHostKeys(deploy.Pinned{in.host: in.hostKeys}))
	dir, release, err := clone.Checkout(ctx, key.String(), "main", pushed)
	require.NoError(t, err)
	readme, err := os.ReadFile(filepath.Join(dir, "README"))
	release()
	require.NoError(t, err)
	require.Equal(t, pushContent, string(readme))

	t.Log("the deploy key cannot push")
	require.Error(t, pushWithKey(t, address, private, in.hostKeys),
		"a deploy key tau installed pushed to %s", key)
	require.Equal(t, http.StatusNotFound, status(t, in.branchURL(key, "deploy-key-wrote-this"), in.asAdmin),
		"the push with the deploy key reported an error and still created the branch")
	require.Equal(t, http.StatusOK, status(t, in.branchURL(key, "main"), in.asAdmin),
		"the branch probe cannot see the branch that was pushed, so its 404 above proves nothing")

	t.Log("removal")
	if !in.noHooks {
		// Gogs answers 204 for a hook it does not have; no driver may pass
		// that on as a removal.
		require.Error(t, s.RemovePushHook(ctx, key, "999999"), "a hook the forge does not have was removed")
		require.NoError(t, s.RemovePushHook(ctx, key, hook.Handle))
		hooks, err := s.ListPushHooks(ctx, key)
		require.NoError(t, err)
		require.Empty(t, hooks)
	}
	require.NoError(t, s.RemoveDeployKey(ctx, key, dk))
	keys, err = s.ListDeployKeys(ctx, key)
	require.NoError(t, err)
	require.False(t, holdsKey(keys, dk, public))
	require.NoError(t, s.Delete(ctx, key))
	_, err = s.Access(ctx, key)
	require.ErrorAs(t, err, &missing, "a deleted repository is gone")
}

const pushContent = "pushed by the tau live harness\n"

func forSession(t *testing.T, in *instance, key repokey.Key) *forge.Session {
	t.Helper()
	s, err := in.reg.For(key, in.user)
	require.NoError(t, err)
	return s
}

func access(t *testing.T, in *instance, key repokey.Key) forge.Access {
	t.Helper()
	a, err := forSession(t, in, key).Access(t.Context(), key)
	require.NoError(t, err)
	return a
}

func keyOf(t *testing.T, in *instance, full string) repokey.Key {
	t.Helper()
	k, err := repokey.Parse("http://" + in.host + "/" + full + ".git")
	require.NoError(t, err)
	return k
}

func holdsKey(keys []forge.InstalledDeployKey, h forge.Handle, want gossh.PublicKey) bool {
	for _, k := range keys {
		got, _, _, _, err := gossh.ParseAuthorizedKey([]byte(k.PublicKey))
		if k.Handle == h && err == nil && bytes.Equal(got.Marshal(), want.Marshal()) {
			return true
		}
	}
	return false
}

// sshAddress is the forge's own ssh clone URL as an address: the account is
// the URL's user, which is the forge's to say.
func sshAddress(t *testing.T, raw string) forge.Address {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err, "the forge's ssh address %q", raw)
	require.Equal(t, "ssh", u.Scheme, "the forge's ssh address %q", raw)
	require.NotEmpty(t, u.User.Username(), "the forge's ssh address %q names no account", raw)
	return forge.Address{URL: raw, SSHAccount: u.User.Username()}
}

type deployKey string

func (k deployKey) DeployKey(context.Context, string) (string, error) { return string(k), nil }

func push(t *testing.T, remote string, auth transport.AuthMethod) string {
	t.Helper()
	st, commit := gittest.Repository(t, map[string]string{"README": pushContent})
	repo, err := gogit.Open(st.(storage.Storer), nil)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{remote}})
	require.NoError(t, err)
	require.NoError(t, repo.PushContext(t.Context(), &gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitconfig.RefSpec{"refs/heads/main:refs/heads/main"},
		Auth:       auth,
	}))
	return commit
}

// pushWithKey tries to push a new branch over ssh with a deploy key, pinned to
// the forge's host keys so a refusal is the forge's and not the handshake's.
func pushWithKey(t *testing.T, address forge.Address, private string, hostKeys []gossh.PublicKey) error {
	t.Helper()
	auth, err := gitssh.NewPublicKeys(address.SSHAccount, []byte(private), "")
	require.NoError(t, err)
	auth.HostKeyCallback = func(_ string, _ net.Addr, key gossh.PublicKey) error {
		for _, k := range hostKeys {
			if bytes.Equal(k.Marshal(), key.Marshal()) {
				return nil
			}
		}
		return errors.New("the forge answered with a host key nobody pinned")
	}
	st, _ := gittest.Repository(t, map[string]string{"README": "a deploy key must not write this\n"})
	repo, err := gogit.Open(st.(storage.Storer), nil)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{address.URL}})
	require.NoError(t, err)
	err = repo.PushContext(t.Context(), &gogit.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitconfig.RefSpec{"refs/heads/main:refs/heads/deploy-key-wrote-this"},
		Auth:       auth,
	})
	t.Logf("push with the deploy key: %v", err)
	return err
}

// receiver is the callback a forge delivers to: every request it gets, as the
// header and the exact body bytes, in order.
type receiver struct {
	path string
	mu   sync.Mutex
	got  []forge.Delivery
}

func receive(t *testing.T, listen string) *receiver {
	t.Helper()
	rx := &receiver{path: "/hook/" + fmt.Sprint(time.Now().UnixNano())}
	l, err := net.Listen("tcp", listen)
	require.NoError(t, err)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || r.URL.Path != rx.path {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		rx.mu.Lock()
		rx.got = append(rx.got, forge.Delivery{Header: r.Header.Clone(), Body: body})
		rx.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return rx
}

// await hands each delivery to judge until it says it was the one, or two
// minutes pass — a forge delivers from a queue, not inline with the push.
func (rx *receiver) await(t *testing.T, judge func(forge.Delivery) bool) bool {
	t.Helper()
	seen := 0
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		rx.mu.Lock()
		fresh := rx.got[seen:]
		seen = len(rx.got)
		rx.mu.Unlock()
		for _, d := range fresh {
			if judge(d) {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// container is one forge server this test started, through the repo's own
// docker backend.
type container struct {
	name string
	out  output
}

type output struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// forgeImages is every image a live test starts. `make forge-images` pulls
// them before `make test-forges` starts its timed suite, so a cold image cache
// costs the gate the download and never the suite's timeout: GitLab alone is
// 1.7 GiB. start refuses an image that is not here.
var forgeImages = []string{giteaImage, forgejoImage, gitlabImage, gerritImage, onedevImage, gogsImage}

func TestPullEveryForgeImage(t *testing.T) {
	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err)
	for _, image := range forgeImages {
		pulled := time.Now()
		require.NoError(t, backend.Image(image).Pull(t.Context()), "pulling %s", image)
		t.Logf("%s pulled in %s", image, time.Since(pulled).Round(time.Second))
	}
}

func start(t *testing.T, cfg *core.ContainerConfig) *container {
	t.Helper()
	require.Contains(t, forgeImages, cfg.Image, "make forge-images does not pull %s, so the suite's timeout would pay for it", cfg.Image)
	sweep(t)
	backend, err := docker.New(core.DockerConfig{})
	require.NoError(t, err)
	pulled := time.Now()
	require.NoError(t, backend.Image(cfg.Image).Pull(t.Context()), "pulling %s", cfg.Image)
	t.Logf("%s pulled in %s", cfg.Image, time.Since(pulled).Round(time.Second))

	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	t.Cleanup(func() { cli.Close() })
	cfg.Labels = map[string]string{ownerLabel: fmt.Sprint(os.Getpid())}
	id, err := backend.Create(t.Context(), cfg)
	require.NoError(t, err)
	c := &container{name: string(id)}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("%s logs, tail:\n%s", cfg.Image, tail(c.logs(), 60))
		}
		ctx := context.Background()
		backend.Stop(ctx, id)
		if err := remove(ctx, cli, c.name); err != nil {
			t.Errorf("removing %s: %v", c.name, err)
		}
	})
	require.NoError(t, backend.Start(t.Context(), id))

	// Logs follows until the container exits, so a server's log is read for as
	// long as it runs, and finished before the container is removed.
	follow, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	rc, err := backend.Logs(follow, id)
	require.NoError(t, err)
	read := make(chan struct{})
	go func() {
		defer close(read)
		defer rc.Close()
		stdcopy.StdCopy(&c.out, &c.out, rc)
	}()
	t.Cleanup(func() {
		stop()
		<-read
	})
	return c
}

// ownerLabel names the test process that started a container.
const ownerLabel = "taubyte.forge-live.owner"

// sweep removes containers an earlier run started and did not live to remove: a
// -timeout panic or a killed run runs no Cleanup. Only containers whose owner
// process is gone are removed, so two runs on one host leave each other alone.
func sweep(t *testing.T) {
	t.Helper()
	cli, err := client.New(client.FromEnv)
	require.NoError(t, err)
	defer cli.Close()
	ctx := context.Background()
	res, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: client.Filters{}.Add("label", ownerLabel)})
	require.NoError(t, err)
	for _, c := range res.Items {
		var pid int
		fmt.Sscan(c.Labels[ownerLabel], &pid)
		if pid > 0 && syscall.Kill(pid, 0) == nil {
			continue
		}
		t.Logf("removing %s (%s), left behind by test process %s", c.ID[:12], c.Image, c.Labels[ownerLabel])
		require.NoError(t, remove(ctx, cli, c.ID))
	}
}

// remove deletes a container the harness started, with the anonymous volumes
// its image declares. Docker keeps those unless asked, and every forge image
// declares some, so each removal without them left a forge's data on the host.
func remove(ctx context.Context, cli *client.Client, id string) error {
	_, err := cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	return err
}

func (c *container) logs() string { return c.out.String() }

func (c *container) lines(tag string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(c.logs()))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == tag {
			out = append(out, "")
		} else if rest, ok := strings.CutPrefix(line, tag+" "); ok {
			out = append(out, strings.TrimSpace(rest))
		}
	}
	return out
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// api is a plain JSON call for SETTING UP a forge — accounts, namespaces,
// fixtures — which is the test's business and not the driver's, so it does not
// go through one.
func api(t *testing.T, method, url string, header http.Header, in, out any) int {
	t.Helper()
	var body io.Reader
	if in != nil {
		enc, err := json.Marshal(in)
		require.NoError(t, err)
		body = bytes.NewReader(enc)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	require.NoError(t, err)
	for k, v := range header {
		req.Header[k] = v
	}
	if in != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if resp.StatusCode >= 300 {
		t.Fatalf("setup: %s %s answered %d: %s", method, url, resp.StatusCode, raw)
	}
	if out != nil {
		require.NoError(t, json.Unmarshal(raw, out), "setup: %s %s answered %s", method, url, raw)
	}
	return resp.StatusCode
}

// eventually polls ok until it holds or limit passes, and says how long it took.
func eventually(t *testing.T, what string, limit time.Duration, ok func() bool) time.Duration {
	t.Helper()
	began := time.Now()
	for time.Since(began) < limit {
		if ok() {
			took := time.Since(began).Round(time.Second)
			t.Logf("%s after %s", what, took)
			return took
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("%s: not within %s", what, limit)
	return 0
}

func basic(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

func fetch(t *testing.T, url string, header http.Header) string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header = header.Clone()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return resp.Status + " (body unreadable: " + err.Error() + ")"
	}
	return resp.Status + " " + string(b)
}

func status(t *testing.T, url string, header http.Header) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header = header.Clone()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

// fixed is the one address a test's forge serves its repository at.
type fixed forge.Address

func (f fixed) Address(string, forge.Protocol) (forge.Address, error) { return forge.Address(f), nil }
