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
	"github.com/taubyte/tau/pkg/forge/gitea"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// The images, pinned. Both are the ROOTLESS variants: one process, the
// server's own built-in ssh, no s6 and no openssh, which is the least a test
// has to know about an image to run it.
const (
	giteaImage   = "gitea/gitea:1.25.3-rootless"
	forgejoImage = "codeberg.org/forgejo/forgejo:12.0.4-rootless"
)

func TestARealGiteaAnswersTheGiteaDriver(t *testing.T) { walk(t, bootGitea(t, "gitea", giteaImage)) }

func TestARealForgejoAnswersTheGiteaDriver(t *testing.T) {
	walk(t, bootGitea(t, "forgejo", forgejoImage))
}

// giteaScript is the container's command. It starts the server, waits for it,
// then makes the administrator WITH THE SERVER'S OWN CLI — the one way to get a
// first account and token that is not a browser — and writes what the test
// needs to its log, tagged: the admin's token and the ssh host keys the server
// generated, read off its own disk rather than off a handshake the test would
// then be trusting on first use.
const giteaScript = `set -e
BIN=$(command -v forgejo || command -v gitea)
INI=${GITEA_APP_INI:-/etc/gitea/app.ini}
"$BIN" -c "$INI" web &
pid=$!
until wget -q -O /dev/null "http://127.0.0.1:${TAU_HTTP_PORT}/api/healthz"; do sleep 1; done
"$BIN" -c "$INI" admin user create --admin --username warden --password warden-pass-1234 \
	--email warden@example.invalid --must-change-password=false >/dev/null
echo "TAU-ADMIN-TOKEN $("$BIN" -c "$INI" admin user generate-access-token --username warden \
	--token-name harness --scopes all --raw)"
for f in $(find /var/lib/gitea -name '*.pub' 2>/dev/null); do echo "TAU-HOST-KEY $(cat "$f")"; done
echo TAU-READY
wait $pid
`

func bootGitea(t *testing.T, name, image string) *instance {
	t.Helper()
	httpPort, sshPort := freePort(t), freePort(t)
	host := fmt.Sprintf("127.0.0.1:%d", httpPort)
	base := "http://" + host

	// HOST NETWORKING, so the server's clone URLs, its ssh port and the hook
	// receiver are all the same loopback addresses on both sides, and the
	// webhook allow-list can say "loopback" and nothing wider.
	c := start(t, &core.ContainerConfig{
		Image:   image,
		Command: []string{"/bin/sh", "-c", giteaScript},
		Env: []string{
			"TAU_HTTP_PORT=" + fmt.Sprint(httpPort),
			"GITEA__security__INSTALL_LOCK=true",
			"GITEA__database__DB_TYPE=sqlite3",
			"GITEA__server__DOMAIN=127.0.0.1",
			"GITEA__server__HTTP_ADDR=127.0.0.1",
			"GITEA__server__HTTP_PORT=" + fmt.Sprint(httpPort),
			"GITEA__server__ROOT_URL=" + base + "/",
			"GITEA__server__START_SSH_SERVER=true",
			"GITEA__server__SSH_DOMAIN=127.0.0.1",
			"GITEA__server__SSH_PORT=" + fmt.Sprint(sshPort),
			"GITEA__server__SSH_LISTEN_HOST=127.0.0.1",
			"GITEA__server__SSH_LISTEN_PORT=" + fmt.Sprint(sshPort),
			"GITEA__webhook__ALLOWED_HOST_LIST=loopback",
			"GITEA__repository__DEFAULT_BRANCH=main",
			"GITEA__log__LEVEL=Warn",
		},
		Network: &core.NetworkConfig{Mode: "host"},
	})
	eventually(t, name+" ready", 3*time.Minute, func() bool { return len(c.lines("TAU-READY")) > 0 })

	tokens := c.lines("TAU-ADMIN-TOKEN")
	require.Len(t, tokens, 1, "the server's CLI printed no admin token")
	admin := tokens[0]
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
		"username": user, "email": user + "@example.invalid", "password": password, "must_change_password": false,
	}, nil)
	var minted struct {
		SHA1 string `json:"sha1"`
	}
	req := http.Header{}
	req.Set("Authorization", "Basic "+basic(user, password))
	api(t, http.MethodPost, base+"/api/v1/users/"+user+"/tokens", req, map[string]any{
		"name": "harness", "scopes": []string{"write:repository", "write:user", "read:organization"},
	}, &minted)
	require.NotEmpty(t, minted.SHA1)

	api(t, http.MethodPost, base+"/api/v1/orgs", asAdmin, map[string]any{"username": "acme-live"}, nil)
	for _, r := range []struct {
		name    string
		private bool
	}{{"public", false}, {"secret", true}} {
		api(t, http.MethodPost, base+"/api/v1/user/repos", asAdmin, map[string]any{
			"name": r.name, "private": r.private, "auto_init": true,
		}, nil)
	}

	drv, err := gitea.New(host, gitea.Plaintext(), gitea.Membership(admin))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowInsecureCallback: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)

	in := &instance{
		name: name, host: host, reg: reg,
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
