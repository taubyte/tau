//go:build docker_integration

package live_test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gitlab"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const gitlabImage = "gitlab/gitlab-ce:18.4.2-ce.0"

func TestARealGitLabAnswersTheGitLabDriver(t *testing.T) { walk(t, bootGitLab(t)) }

// gitlabScript runs the image's own init (its CMD), waits for the web tier, and
// creates the administrator's token with gitlab-rails — GitLab's own console,
// and the documented way to make a token without a browser. The token's value
// is the test's, handed in, so nothing secret has to be read back out of a log;
// the host keys are read off the server's own disk.
const gitlabScript = `/assets/init-container &
pid=$!
until curl -fs -o /dev/null http://127.0.0.1/users/sign_in; do sleep 5; done
gitlab-rails runner "t = User.find_by_username('root').personal_access_tokens.create!(name: 'harness', scopes: [:api], expires_at: 7.days.from_now); t.set_token(ENV['TAU_ADMIN_TOKEN']); t.save!"
for f in /etc/gitlab/ssh_host_*_key.pub; do echo "TAU-HOST-KEY $(cat "$f")"; done
echo TAU-READY
wait $pid
`

func bootGitLab(t *testing.T) *instance {
	t.Helper()
	httpPort, sshPort := freePort(t), freePort(t)
	host := fmt.Sprintf("127.0.0.1:%d", httpPort)
	base := "http://" + host
	admin := "glpat-" + randomHex(t, 16)

	// BRIDGED AND NOT HOST-NETWORKED: omnibus binds sshd on 22 and a dozen
	// internal ports, any of which the host may already hold. The ports it
	// serves are published on loopback, and external_url and the shell port
	// name the published ones, so its clone URLs are addresses on this side.
	omnibus := fmt.Sprintf(`external_url '%s'
nginx['listen_port'] = 80
nginx['listen_https'] = false
gitlab_rails['gitlab_shell_ssh_port'] = %d
puma['worker_processes'] = 0
sidekiq['concurrency'] = 5
prometheus_monitoring['enable'] = false
gitlab_kas['enable'] = false
registry['enable'] = false
gitlab_rails['usage_ping_enabled'] = false
`, base, sshPort)

	booted := time.Now()
	c := start(t, &core.ContainerConfig{
		Image:   gitlabImage,
		Command: []string{"/bin/bash", "-c", gitlabScript},
		Env:     []string{"GITLAB_OMNIBUS_CONFIG=" + omnibus, "TAU_ADMIN_TOKEN=" + admin},
		Network: &core.NetworkConfig{Mode: "bridge", PortMappings: []core.PortMapping{
			{HostIP: "127.0.0.1", HostPort: httpPort, ContainerPort: 80, Protocol: "tcp"},
			{HostIP: "127.0.0.1", HostPort: sshPort, ContainerPort: 22, Protocol: "tcp"},
		}},
	})
	eventually(t, "gitlab ready", 15*time.Minute, func() bool { return len(c.lines("TAU-READY")) > 0 })
	t.Logf("gitlab boot, pull excluded, to a usable admin token: %s", time.Since(booted).Round(time.Second))

	var hostKeys []gossh.PublicKey
	for _, line := range c.lines("TAU-HOST-KEY") {
		k, _, _, _, err := gossh.ParseAuthorizedKey([]byte(line))
		require.NoError(t, err, "host key %q", line)
		hostKeys = append(hostKeys, k)
	}
	require.NotEmpty(t, hostKeys)

	asAdmin := http.Header{"PRIVATE-TOKEN": {admin}}
	// A hook to this test's receiver is a request to the local network, which
	// GitLab refuses by default. It is an instance setting, so it is set here.
	api(t, http.MethodPut, base+"/api/v4/application/settings?allow_local_requests_from_web_hooks_and_services=true", asAdmin, nil, nil)

	const user = "tau-dev"
	type createdJSON struct {
		ID int64 `json:"id"`
	}
	var created createdJSON
	api(t, http.MethodPost, base+"/api/v4/users", asAdmin, map[string]any{
		"username": user, "name": user, "email": user + "@example.invalid",
		"password": "Dev-" + randomHex(t, 12), "skip_confirmation": true,
	}, &created)
	type personalToken struct {
		Token string `json:"token"`
	}
	var pat personalToken
	api(t, http.MethodPost, fmt.Sprintf("%s/api/v4/users/%d/personal_access_tokens", base, created.ID), asAdmin, map[string]any{
		"name": "harness", "scopes": []string{"api"}, "expires_at": time.Now().AddDate(0, 0, 7).Format("2006-01-02"),
	}, &pat)
	require.NotEmpty(t, pat.Token)

	api(t, http.MethodPost, base+"/api/v4/groups", asAdmin, map[string]any{"name": "acme-live", "path": "acme-live"}, nil)
	type testCase struct{ name, visibility string }
	for _, r := range []testCase{{"public", "public"}, {"secret", "private"}} {
		api(t, http.MethodPost, base+"/api/v4/projects", asAdmin, map[string]any{
			"name": r.name, "visibility": r.visibility, "initialize_with_readme": true,
		}, nil)
	}

	drv, err := gitlab.New(host, gitlab.BaseURL(base), gitlab.Deployment(forge.Token(admin)))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)

	in := &instance{
		name: "gitlab", host: host, reg: reg,
		user: forge.Token(pat.Token), userLogin: user,
		pushAs:     &githttp.BasicAuth{Username: user, Password: pat.Token},
		adminLogin: "root", namespace: "acme-live", noHooks: true,
		hostKeys: hostKeys,
		asAdmin:  asAdmin,
		branchURL: func(k repokey.Key, branch string) string {
			return base + "/api/v4/projects/" + url.PathEscape(k.FullName()) + "/repository/branches/" + branch
		},
	}
	in.public, in.private = keyOf(t, in, "root/public"), keyOf(t, in, "root/secret")

	// The container reaches this side through the bridge's gateway, so the
	// receiver listens there and the hook is told that address.
	gw := bridgeGateway(t)
	in.listen = net.JoinHostPort(gw, fmt.Sprint(freePort(t)))
	in.reach = in.listen
	return in
}

// bridgeGateway is docker0's address: where a bridged container reaches the
// host.
func bridgeGateway(t *testing.T) string {
	t.Helper()
	iface, err := net.InterfaceByName("docker0")
	require.NoError(t, err, "a bridged container reaches the host through docker0")
	addrs, err := iface.Addrs()
	require.NoError(t, err)
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil {
			return ip.IP.String()
		}
	}
	t.Fatal("docker0 has no IPv4 address")
	return ""
}

func randomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}
