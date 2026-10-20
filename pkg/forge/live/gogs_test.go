//go:build docker_integration

package live_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gogs"
	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/deploy"
)

// gogsImage is pinned. Its entrypoint prepares /data and then runs the command
// it is given as root, which is how the script below gets to write app.ini
// before the server reads it.
//
// 0.13.3 AND NOT 0.14.0: measured, a clone from 0.14.0 — over its built-in ssh
// server or over the image's OpenSSH, with OpenSSH's client or go-git's —
// starts git-upload-pack on the far side and then delivers nothing, so the
// client waits forever. 0.13.3 answers the same clone.
const gogsImage = "gogs/gogs:0.13.3"

// TestARealGogsAnswersTheGogsDriver walks everything tau asks of a forge
// against a real Gogs — except membership, which Gogs cannot answer and the
// driver therefore does not offer. walk() requires CapMembership of every
// forge, so this is walk() without that step, plus the step that proves the
// absence is an *UnsupportedError rather than a false.
func TestARealGogsAnswersTheGogsDriver(t *testing.T) { walkWithoutMembership(t, bootGogs(t)) }

// gogsScript writes the whole configuration — install lock, sqlite, the
// built-in ssh server, a webhook allow-list wide enough for the loopback
// receiver — starts the server, makes the administrator with the server's own
// CLI, and mints its token over the API with basic auth, the one way Gogs
// offers. Host keys are read off the server's disk rather than a handshake.
const gogsScript = `set -e
mkdir -p /data/gogs/conf /data/gogs/data /data/gogs/log /data/git
cat > /data/gogs/conf/app.ini <<EOF
RUN_USER = git
RUN_MODE = prod
[database]
TYPE = sqlite3
PATH = /data/gogs/data/gogs.db
[repository]
ROOT = /data/git/gogs-repositories
DEFAULT_BRANCH = main
[server]
PROTOCOL = http
DOMAIN = 127.0.0.1
HTTP_ADDR = 127.0.0.1
HTTP_PORT = ${TAU_HTTP_PORT}
EXTERNAL_URL = http://127.0.0.1:${TAU_HTTP_PORT}/
APP_DATA_PATH = /data/gogs/data
START_SSH_SERVER = true
SSH_DOMAIN = 127.0.0.1
SSH_PORT = ${TAU_SSH_PORT}
SSH_LISTEN_HOST = 127.0.0.1
SSH_LISTEN_PORT = ${TAU_SSH_PORT}
[security]
INSTALL_LOCK = true
SECRET_KEY = tau-live-harness-not-a-secret
LOCAL_NETWORK_ALLOWLIST = 127.0.0.1
[log]
MODE = console
ROOT_PATH = /data/gogs/log
LEVEL = Warn
EOF
chown -R git:git /data
cd /app/gogs
export GOGS_CUSTOM=/data/gogs
gosu git ./gogs web --config /data/gogs/conf/app.ini &
pid=$!
until wget -q -O /dev/null "http://127.0.0.1:${TAU_HTTP_PORT}/healthcheck"; do kill -0 $pid; sleep 1; done
gosu git ./gogs admin create-user --admin --name warden --password warden-pass-1234 \
	--email warden@example.invalid --config /data/gogs/conf/app.ini >/dev/null
echo "TAU-ADMIN-TOKEN $(curl -fsS -u warden:warden-pass-1234 -H 'Content-Type: application/json' \
	-d '{"name":"harness"}' "http://127.0.0.1:${TAU_HTTP_PORT}/api/v1/users/warden/tokens" \
	| sed -E 's/.*"sha1":"([0-9a-f]+)".*/\1/')"
for f in $(find /data/gogs/data -name '*.pub' 2>/dev/null); do echo "TAU-HOST-KEY $(cat "$f")"; done
echo TAU-READY
wait $pid
`

func bootGogs(t *testing.T) *instance {
	t.Helper()
	httpPort, sshPort := freePort(t), freePort(t)
	host := fmt.Sprintf("127.0.0.1:%d", httpPort)
	base := "http://" + host

	c := start(t, &core.ContainerConfig{
		Image:   gogsImage,
		Command: []string{"/bin/sh", "-c", gogsScript},
		Env: []string{
			"TAU_HTTP_PORT=" + fmt.Sprint(httpPort),
			"TAU_SSH_PORT=" + fmt.Sprint(sshPort),
		},
		Network: &core.NetworkConfig{Mode: "host"},
	})
	eventually(t, "gogs ready", 3*time.Minute, func() bool { return len(c.lines("TAU-READY")) > 0 })

	tokens := c.lines("TAU-ADMIN-TOKEN")
	require.Len(t, tokens, 1, "the server printed no admin token")
	admin := tokens[0]
	require.NotEmpty(t, admin)
	var hostKeys []gossh.PublicKey
	for _, line := range c.lines("TAU-HOST-KEY") {
		k, _, _, _, err := gossh.ParseAuthorizedKey([]byte(line))
		require.NoError(t, err, "host key %q", line)
		hostKeys = append(hostKeys, k)
	}
	require.NotEmpty(t, hostKeys, "the server generated no ssh host key the test could find")

	asAdmin := http.Header{"Authorization": {"token " + admin}}
	const user, password = "tau-dev", "tau-dev-pass-1234"
	api(t, http.MethodPost, base+"/api/v1/admin/users", asAdmin, map[string]any{
		"username": user, "email": user + "@example.invalid", "password": password, "send_notify": false,
	}, nil)
	var minted struct {
		SHA1 string `json:"sha1"`
	}
	asUser := http.Header{"Authorization": {"Basic " + basic(user, password)}}
	api(t, http.MethodPost, base+"/api/v1/users/"+user+"/tokens", asUser, map[string]any{"name": "harness"}, &minted)
	require.NotEmpty(t, minted.SHA1)

	api(t, http.MethodPost, base+"/api/v1/admin/users/warden/orgs", asAdmin, map[string]any{"username": "acme-live"}, nil)
	for _, r := range []struct {
		name    string
		private bool
	}{{"public", false}, {"secret", true}} {
		api(t, http.MethodPost, base+"/api/v1/user/repos", asAdmin, map[string]any{
			"name": r.name, "private": r.private, "auto_init": true, "readme": "Default",
		}, nil)
	}

	drv, err := gogs.New(host, gogs.Plaintext())
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowInsecureCallback: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)

	in := &instance{
		name: "gogs", host: host, reg: reg,
		user: forge.Token(minted.SHA1), userLogin: user,
		pushAs:     &githttp.BasicAuth{Username: user, Password: minted.SHA1},
		adminLogin: "warden", namespace: "acme-live",
		hostKeys: hostKeys,
		asAdmin:  asAdmin,
		branchURL: func(k repokey.Key, branch string) string {
			return base + "/api/v1/repos/" + k.Owner + "/" + k.Name + "/branches/" + branch
		},
	}
	in.public, in.private = keyOf(t, in, "warden/public"), keyOf(t, in, "warden/secret")
	in.listen = fmt.Sprintf("127.0.0.1:%d", freePort(t))
	in.reach = in.listen
	return in
}

// walkWithoutMembership is walk() with its membership step replaced by the
// assertion that the question is refused as unsupported.
func walkWithoutMembership(t *testing.T, in *instance) {
	ctx := t.Context()

	sess, err := in.reg.Host(in.host, in.user)
	require.NoError(t, err)
	require.NoError(t, sess.Require(
		forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapDeployKeys,
		forge.CapPushHooks, forge.CapCloneAddr, forge.CapSignatures))
	require.False(t, sess.Capabilities().Has(forge.CapMembership))

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

	t.Log("membership is refused, not answered")
	isMember, err := in.reg.IsActiveMember(ctx, in.host, in.namespace, in.adminLogin)
	var unsupported *forge.UnsupportedError
	require.ErrorAs(t, err, &unsupported)
	require.False(t, isMember)

	t.Log("create")
	name := fmt.Sprintf("live-%d", time.Now().UnixNano())
	key := keyOf(t, in, in.userLogin+"/"+name)
	s := forSession(t, in, key)
	created, err := s.Create(ctx, key, forge.CreateOptions{Description: "tau live harness", Private: true})
	require.NoError(t, err)
	require.Equal(t, key, created.Key)
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

	t.Log("hook")
	rx := receive(t, in.listen)
	callback, err := url.Parse("http://" + in.reach + rx.path)
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	hook, err := s.RegisterPushHook(ctx, key, callback, secret)
	require.NoError(t, err)
	hooks, err := s.ListPushHooks(ctx, key)
	require.NoError(t, err)
	require.Contains(t, hooks, forge.InstalledPushHook{Handle: hook.Handle, Callback: callback.String()})

	t.Log("push")
	pushed := push(t, created.HTTPS, in.pushAs)

	t.Log("delivery")
	verified := rx.await(t, func(d forge.Delivery) bool {
		p, err := in.reg.VerifyPush(hook, d)
		if errors.Is(err, forge.ErrNotAPush) {
			return false
		}
		require.NoError(t, err, "a delivery gogs signed and sent failed verification")
		require.Equal(t, forge.Push{Key: key, Ref: "refs/heads/main", Commit: pushed}, p)

		tampered := forge.Delivery{Header: d.Header, Body: bytes.Replace(d.Body, []byte(pushed), []byte(strings.Repeat("e", len(pushed))), 1)}
		_, err = in.reg.VerifyPush(hook, tampered)
		require.Error(t, err, "the same delivery naming another commit verified")
		return true
	})
	require.True(t, verified, "gogs never delivered the push")

	t.Log("clone with the deploy key")
	repo, err := s.Repository(ctx, key)
	require.NoError(t, err)
	address := sshAddress(t, repo.SSH)
	clone := deploy.NewClone(deployKey(private),
		deploy.WithAddress(func(string, forge.Protocol) (forge.Address, error) { return address, nil }),
		deploy.WithHostKeys(func(host string) []gossh.PublicKey {
			if host == in.host {
				return in.hostKeys
			}
			return nil
		}))
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
	require.Error(t, s.RemovePushHook(ctx, key, "999999"),
		"gogs answers 204 for a hook it does not have; the driver must not pass that on as a removal")
	require.NoError(t, s.RemovePushHook(ctx, key, hook.Handle))
	hooks, err = s.ListPushHooks(ctx, key)
	require.NoError(t, err)
	require.Empty(t, hooks)
	require.NoError(t, s.RemoveDeployKey(ctx, key, dk))
	keys, err = s.ListDeployKeys(ctx, key)
	require.NoError(t, err)
	require.False(t, holdsKey(keys, dk, public))
	require.NoError(t, s.Delete(ctx, key))
	_, err = s.Access(ctx, key)
	require.ErrorAs(t, err, &missing, "a deleted repository is gone")
}
