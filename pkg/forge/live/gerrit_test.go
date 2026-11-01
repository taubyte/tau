//go:build docker_integration

package live_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gerrit"
)

// gerritImage is pinned. Its site is initialised in the image in
// DEVELOPMENT_BECOME_ANY_ACCOUNT mode, with an administrator "admin" whose HTTP
// password is "secret", and it bundles the plugins this driver leans on:
// delete-project (Delete), singleusergroup (the "user:<name>" owner group
// Create names) and webhooks (which signs nothing, and is why the hook face is
// unsupported — see the gerrit package comment).
const gerritImage = "gerritcodereview/gerrit:3.12.3"

func TestARealGerritAnswersTheGerritDriver(t *testing.T) {
	ctx := t.Context()
	port := freePort(t)
	host := fmt.Sprintf("127.0.0.1:%d", port)
	base := "http://" + host

	// BRIDGE NETWORKING WITH ONE MAPPED PORT. The image's entrypoint takes no
	// script, so its sshd listens on 29418 whatever this says; on the host
	// network that would collide with any other Gerrit on the machine. Nothing
	// here uses ssh.
	start(t, &core.ContainerConfig{
		Image: gerritImage,
		Env:   []string{"CANONICAL_WEB_URL=" + base + "/"},
		Network: &core.NetworkConfig{Mode: "bridge", PortMappings: []core.PortMapping{
			{HostPort: port, ContainerPort: 8080, HostIP: "127.0.0.1"},
		}},
	})
	eventually(t, "gerrit ready", 4*time.Minute, func() bool {
		resp, err := http.Get(base + "/config/server/version")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	asAdmin := http.Header{}
	asAdmin.Set("Authorization", "Basic "+basic("admin", "secret"))
	allow := map[string]any{"global:Registered-Users": map[string]string{"action": "ALLOW"}}
	// Registered users may create projects and delete their own, and may push
	// commits authored by somebody else — the harness commit is gittest's.
	api(t, http.MethodPost, base+"/a/projects/All-Projects/access", asAdmin, map[string]any{"add": map[string]any{
		"GLOBAL_CAPABILITIES": map[string]any{"permissions": map[string]any{
			"createProject":                   map[string]any{"rules": allow},
			"delete-project-deleteOwnProject": map[string]any{"rules": allow},
		}},
		"refs/*": map[string]any{"permissions": map[string]any{
			"forgeAuthor":    map[string]any{"rules": allow},
			"forgeCommitter": map[string]any{"rules": allow},
		}},
		// OWNERS MAY PUSH, which Gerrit does not grant by default: measured,
		// an owner's push of a new commit is "prohibited by Gerrit: update for
		// creating new commit object not permitted". Ownership is the right to
		// write the access config that grants Push, which is why the driver
		// reads it as admin; this rule is that grant, written once.
		"refs/heads/*": map[string]any{"permissions": map[string]any{
			"push": map[string]any{"rules": map[string]any{"global:Project-Owners": map[string]string{"action": "ALLOW"}}},
		}},
	}}, nil)

	const user, password = "tau-dev", "tau-dev-pass-1234"
	api(t, http.MethodPut, base+"/a/accounts/"+user, asAdmin, map[string]any{
		"name": "tau dev", "email": user + "@example.com", "http_password": password,
	}, nil)
	api(t, http.MethodPut, base+"/a/projects/warden%2Fpublic", asAdmin, map[string]any{"create_empty_commit": true}, nil)
	api(t, http.MethodPut, base+"/a/projects/warden%2Fsecret", asAdmin, map[string]any{"create_empty_commit": true}, nil)
	owners := map[string]any{"permissions": map[string]any{"read": map[string]any{
		"exclusive": true, "rules": map[string]any{"global:Project-Owners": map[string]string{"action": "ALLOW"}},
	}}}
	api(t, http.MethodPost, base+"/a/projects/warden%2Fsecret/access", asAdmin, map[string]any{"add": map[string]any{
		"refs/*": owners, "refs/heads/*": owners, "refs/tags/*": owners, "refs/meta/version": owners,
	}}, nil)
	// The creator of a group is its first member.
	api(t, http.MethodPut, base+"/a/groups/acme-live", asAdmin, map[string]any{}, nil)

	drv, err := gerrit.New(host, gerrit.Plaintext(), gerrit.Membership("admin:secret"))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowInsecureCallback: true}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	in := &instance{name: "gerrit", host: host, reg: reg, user: forge.Token(user + ":" + password), userLogin: user}

	sess, err := reg.Host(host, in.user)
	require.NoError(t, err)
	require.NoError(t, sess.Require(forge.CapList, forge.CapCreate, forge.CapDelete, forge.CapCloneAddr, forge.CapMembership))
	for _, c := range []forge.Capability{forge.CapDeployKeys, forge.CapPushHooks, forge.CapSignatures, forge.CapPushProbe} {
		var un *forge.UnsupportedError
		require.ErrorAs(t, sess.Require(c), &un, "%s", c)
	}

	t.Log("identity")
	who, err := sess.Whoami(ctx)
	require.NoError(t, err)
	require.Equal(t, user, who.Login)
	require.NotEmpty(t, who.Subject)
	bad, err := reg.Host(host, forge.Token(user+":not-the-password"))
	require.NoError(t, err)
	_, err = bad.Whoami(ctx)
	var credErr *forge.CredentialError
	require.ErrorAs(t, err, &credErr, "a password the forge never issued must read as a refused credential")

	t.Log("access")
	public, private := keyOf(t, in, "warden/public"), keyOf(t, in, "warden/secret")
	require.Equal(t, forge.AccessRead, access(t, in, public), "a public project the user does not own is readable and nothing more")
	_, err = forSession(t, in, private).Access(ctx, private)
	var missing *forge.NotFoundError
	require.ErrorAs(t, err, &missing, "a project the user cannot see is indistinguishable from one that does not exist")

	t.Log("membership")
	isMember, err := reg.IsActiveMember(ctx, host, "acme-live", "admin")
	require.NoError(t, err)
	require.True(t, isMember)
	isMember, err = reg.IsActiveMember(ctx, host, "acme-live", user)
	require.NoError(t, err, "a non-member is a definitive (false, nil)")
	require.False(t, isMember)
	_, err = reg.IsActiveMember(ctx, host, "no-such-group", "admin")
	require.Error(t, err, "a group nobody can see is not a group with no members")

	t.Log("create")
	name := fmt.Sprintf("live-%d", time.Now().UnixNano())
	key := keyOf(t, in, user+"/"+name)
	s := forSession(t, in, key)
	created, err := s.Create(ctx, key, forge.CreateOptions{Description: "tau live harness", Private: true})
	require.NoError(t, err)
	t.Cleanup(func() { s.Delete(t.Context(), key) })
	require.Equal(t, key, created.Key)
	require.True(t, created.Private)
	require.Equal(t, forge.AccessAdmin, access(t, in, key), "the creator owns what it created")
	escaped := url.PathEscape(key.FullName())
	require.Equal(t, http.StatusNotFound, status(t, base+"/projects/"+escaped, http.Header{}),
		"a project created private is readable by anonymous")

	t.Log("list")
	listed := false
	require.NoError(t, s.ListRepositories(ctx, func(r forge.Repository) bool {
		listed = listed || r.Key == key
		return !listed
	}))
	require.True(t, listed, "%s was created by this credential and its listing does not name it", key)

	t.Log("hook is unsupported")
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	_, err = s.RegisterPushHook(ctx, key, &url.URL{Scheme: "http", Host: "127.0.0.1:1", Path: "/hook"}, secret)
	var un *forge.UnsupportedError
	require.ErrorAs(t, err, &un)

	t.Log("push over the address the forge named")
	addr, err := s.CloneAddress(key, forge.HTTPS)
	require.NoError(t, err)
	require.Equal(t, created.HTTPS, addr.URL)
	pushed := push(t, created.HTTPS, &githttp.BasicAuth{Username: addr.HTTPUsername, Password: password})
	branch := fetch(t, base+"/a/projects/"+escaped+"/branches/main", asAdmin)
	require.True(t, strings.Contains(branch, pushed), "the pushed commit is not main's head: %s", branch)

	t.Log("removal")
	require.NoError(t, s.Delete(ctx, key))
	_, err = s.Access(ctx, key)
	require.True(t, errors.As(err, &missing), "a deleted project is gone: %v", err)
}
