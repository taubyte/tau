//go:build docker_integration

package live_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gogs"
	"github.com/taubyte/tau/pkg/git/repokey"
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
// against a real Gogs. Membership is absent: Gogs cannot answer it and the
// driver does not offer it, so walk asserts it is refused.
func TestARealGogsAnswersTheGogsDriver(t *testing.T) { walk(t, bootGogs(t)) }

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
		adminLogin: "warden", namespace: "acme-live", noMembership: true,
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
