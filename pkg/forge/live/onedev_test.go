//go:build docker_integration

package live_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/onedev"
	"github.com/taubyte/tau/pkg/git/repokey"
)

const onedevImage = "1dev/server:16.9.4"

// TestARealOneDevAnswersTheOneDevDriver is the onedev driver against OneDev.
//
// IT IS NOT walk, AND THE DIFFERENCE IS OneDev's, NOT THE TEST'S. walk
// requires deploy keys, which OneDev has no spelling of, and OneDev creates
// projects under a parent project rather than a user namespace. Membership is
// absent too, and so are hooks: OneDev signs nothing (X-OneDev-Signature is the
// stored secret replayed), and tau accepts no delivery it cannot verify by a
// signature over the body. Each absent face is asserted to be refused.
func TestARealOneDevAnswersTheOneDevDriver(t *testing.T) {
	ctx := t.Context()
	od := bootOneDev(t)
	in := od.in

	sess, err := in.reg.Host(in.host, in.user)
	require.NoError(t, err)
	require.NoError(t, sess.Require(
		forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapCloneAddr))
	var unsupported *forge.UnsupportedError
	for _, absent := range []forge.Capability{forge.CapDeployKeys, forge.CapMembership, forge.CapPushHooks, forge.CapSignatures} {
		require.ErrorAs(t, sess.Require(absent), &unsupported, string(absent))
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
		"a project every user may read, on which this one holds no grant, is readable and nothing more")
	require.Equal(t, forge.AccessWrite, access(t, in, od.writable))
	require.Equal(t, forge.AccessNone, access(t, in, od.noCode),
		"a project this user reaches without code access is nothing a builder can use")
	_, err = forSession(t, in, in.private).Access(ctx, in.private)
	var missing *forge.NotFoundError
	require.ErrorAs(t, err, &missing, "a private project the user cannot see is indistinguishable from one that does not exist")

	t.Log("create")
	name := fmt.Sprintf("live-%d", time.Now().UnixNano())
	key := keyOf(t, in, in.namespace+"/"+name)
	s := forSession(t, in, key)
	created, err := s.Create(ctx, key, forge.CreateOptions{Description: "tau live harness", Private: true})
	require.NoError(t, err)
	require.Equal(t, key, created.Key, "the key is derived from the forge's answer, and the forge made what was asked")
	require.True(t, created.Private)
	t.Cleanup(func() { s.Delete(context.Background(), key) })
	require.Equal(t, forge.AccessAdmin, access(t, in, key), "the creator manages what it created")

	t.Log("list")
	listed := false
	require.NoError(t, s.ListRepositories(ctx, func(r forge.Repository) bool {
		listed = listed || r.Key == key
		return !listed
	}))
	require.True(t, listed, "%s was created by this credential and its listing does not name it", key)

	t.Log("hooks are refused, not registered")
	callback, err := url.Parse("http://127.0.0.1:1/never")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	_, err = s.RegisterPushHook(ctx, key, callback, secret)
	require.ErrorAs(t, err, &unsupported)

	t.Log("push")
	push(t, created.HTTPS, in.pushAs)

	t.Log("repository")
	repo, err := s.Repository(ctx, key)
	require.NoError(t, err)
	require.Equal(t, "main", repo.DefaultBranch)
	require.Equal(t, created.HTTPS, repo.HTTPS)
	require.NotEmpty(t, repo.SSH)

	t.Log("removal")
	require.NoError(t, s.Delete(ctx, key))
	_, err = s.Access(ctx, key)
	require.ErrorAs(t, err, &missing, "a deleted project is gone")
}

type onedevInstance struct {
	in       *instance
	writable repokey.Key
	noCode   repokey.Key
}

// bootOneDev starts OneDev with its documented unattended setup (the initial_*
// variables), then makes an ordinary user and an access token for it through
// the REST API as the administrator.
//
// BRIDGED AND NOT HOST-NETWORKED: OneDev listens on 6610 and 6611 and cannot
// be told otherwise from the environment, so the two are published on loopback
// ports nothing else holds, and the server URL names the published one so its
// clone URLs are addresses on this side.
func bootOneDev(t *testing.T) *onedevInstance {
	t.Helper()
	httpPort, sshPort := freePort(t), freePort(t)
	host := fmt.Sprintf("127.0.0.1:%d", httpPort)
	base := "http://" + host
	rootPass := "Root-" + randomHex(t, 12)

	booted := time.Now()
	start(t, &core.ContainerConfig{
		Image: onedevImage,
		// The image keeps its data in /opt/onedev and refuses to start without
		// the directory; it lives and dies with the container.
		Command: []string{"/bin/sh", "-c", "mkdir -p /opt/onedev && exec /root/bin/entrypoint.sh"},
		Env: []string{
			"initial_user=root", "initial_password=" + rootPass,
			"initial_email=root@example.invalid",
			"initial_server_url=" + base,
			fmt.Sprintf("initial_ssh_root_url=ssh://127.0.0.1:%d", sshPort),
		},
		Network: &core.NetworkConfig{Mode: "bridge", PortMappings: []core.PortMapping{
			{HostIP: "127.0.0.1", HostPort: httpPort, ContainerPort: 6610, Protocol: "tcp"},
			{HostIP: "127.0.0.1", HostPort: sshPort, ContainerPort: 6611, Protocol: "tcp"},
		}},
	})
	asAdmin := http.Header{"Authorization": {"Basic " + basic("root", rootPass)}}
	eventually(t, "onedev ready", 5*time.Minute, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/~api/users/me", nil)
		require.NoError(t, err)
		req.Header = asAdmin.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	t.Logf("onedev boot, pull excluded, to a usable administrator: %s", time.Since(booted).Round(time.Second))

	const user = "tau-dev"
	var userID int64
	api(t, http.MethodPost, base+"/~api/users", asAdmin, map[string]any{
		"type": "ORDINARY", "name": user, "password": "Dev-" + randomHex(t, 12), "emailAddress": user + "@example.invalid",
	}, &userID)
	api(t, http.MethodPost, base+"/~api/access-tokens", asAdmin, map[string]any{
		"ownerId": userID, "name": "harness", "hasOwnerPermissions": true,
	}, nil)
	var tokens []struct {
		Value string `json:"value"`
	}
	api(t, http.MethodGet, fmt.Sprintf("%s/~api/users/%d/access-tokens", base, userID), asAdmin, nil, &tokens)
	require.Len(t, tokens, 1)
	token := tokens[0].Value
	require.NotEmpty(t, token)

	project := func(name string, parent int64) int64 {
		var id int64
		body := map[string]any{"name": name, "gitPackConfig": map[string]any{}, "codeIndexingSetting": map[string]any{}}
		if parent != 0 {
			body["parentId"] = parent
		}
		api(t, http.MethodPost, base+"/~api/projects", asAdmin, body, &id)
		return id
	}
	role := func(name string) int64 {
		var roles []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		api(t, http.MethodGet, base+"/~api/roles?offset=0&count=100", asAdmin, nil, &roles)
		for _, r := range roles {
			if r.Name == name {
				return r.ID
			}
		}
		t.Fatalf("onedev has no role %q", name)
		return 0
	}
	grant := func(projectID int64, roleName string) {
		api(t, http.MethodPost, base+"/~api/user-authorizations", asAdmin, map[string]any{
			"projectId": projectID, "userId": userID, "roleId": role(roleName),
		}, nil)
	}

	acme := project("acme-live", 0)
	public := project("public", acme)
	// Every logged-in user may read it, and this one holds no grant of its own.
	api(t, http.MethodPost, base+"/~api/base-authorizations", asAdmin, map[string]any{
		"projectId": public, "roleId": role("Code Reader"),
	}, nil)
	project("secret", acme)
	grant(project("pay", acme), "Code Writer")
	grant(project("issues", acme), "Issue Reporter")
	grant(project("work", acme), "Project Owner")

	drv, err := onedev.New(host, onedev.Plaintext())
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{},
		forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)

	in := &instance{
		name: "onedev", host: host, reg: reg,
		user: forge.Token(token), userLogin: user,
		pushAs:     &githttp.BasicAuth{Username: user, Password: token},
		adminLogin: "root", namespace: "acme-live/work",
		asAdmin: asAdmin,
	}
	in.public, in.private = keyOf(t, in, "acme-live/public"), keyOf(t, in, "acme-live/secret")
	return &onedevInstance{in: in, writable: keyOf(t, in, "acme-live/pay"), noCode: keyOf(t, in, "acme-live/issues")}
}
