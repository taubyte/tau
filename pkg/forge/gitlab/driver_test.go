package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// stub is one handler answering exactly what a case needs, so the failure paths
// an honest instance never takes can still be asserted. It is the same shape as
// the conformance instance and a great deal less of it.
func stub(t *testing.T, h http.HandlerFunc, opts ...Option) (forge.Driver, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	d, err := New(host, append([]Option{BaseURL(srv.URL)}, opts...)...)
	require.NoError(t, err)
	return d, srv.URL
}

// stubClient is a signing instance's client, the one type with every client
// face, so a test of any method can reach it.
func stubClient(t *testing.T, h http.HandlerFunc, opts ...Option) (*hookClient, repokey.Key) {
	t.Helper()
	d, base := stub(t, h, append([]Option{Signing()}, opts...)...)
	bound, err := d.Bind(forge.Token("stub-token"))
	require.NoError(t, err)
	key, err := repokey.Parse(base + "/acme/pay.git")
	require.NoError(t, err)
	return bound.(*hookClient), key
}

func answer(t *testing.T, status int, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("writing a stub answer: %v", err)
		}
	}
}

func TestTheDriverKeepsNoCopyOfTheCallersCredential(t *testing.T) {
	const caller = "gl-tok-CALLER-1a2b3c4d"
	const deployment = "gl-tok-DEPLOY-9f8e7d6c"

	d, err := New("gitlab.example", Deployment(forge.Token(deployment)))
	require.NoError(t, err)
	bound, err := d.Bind(forge.Token(caller))
	require.NoError(t, err)
	c := bound.(*client)

	for _, rendering := range []string{
		fmt.Sprintf("%v", *c), fmt.Sprintf("%+v", *c), fmt.Sprintf("%v", c),
		fmt.Sprintf("%v", *c.cfg), fmt.Sprintf("%+v", *c.cfg),
		fmt.Sprintf("%v", d), fmt.Sprintf("%+v", d),
	} {
		assert.NotContains(t, rendering, caller, "a rendering carried the caller's credential: %s", rendering)
		assert.NotContains(t, rendering, deployment, "a rendering carried the deployment's credential: %s", rendering)
	}
}

func TestNewRefusesAHostThatIsNotARepokeySpelling(t *testing.T) {
	for _, host := range []string{"", "GitLab.com", "https://gitlab.com", "gitlab.com:443", "gitlab.com/"} {
		d, err := New(host)
		assert.Nil(t, d, "a refused host still produced a driver")
		assert.Error(t, err, "host %q was accepted", host)
	}
}

func TestNewDefaultsToTheHostOverHTTPS(t *testing.T) {
	d, err := New("gitlab.com")
	require.NoError(t, err)
	assert.Equal(t, "gitlab.com", d.Host())
	assert.Equal(t, "https://gitlab.com", d.(*driver).cfg.base.String())

	// And to no hook faces: an instance is not assumed to sign.
	_, has := d.(forge.Signatures)
	assert.False(t, has, "a driver not told the instance signs claims it can verify deliveries")
}

func TestBaseURLRefusesWhatWouldAimTheCredentialElsewhere(t *testing.T) {
	for name, raw := range map[string]string{
		"not a URL":       "://nope",
		"not a transport": "ftp://gitlab.com",
		"another host":    "https://gitlab.example",
		"no host at all":  "https:///gitlab",
		"no authority":    "https://",
	} {
		_, err := New("gitlab.com", BaseURL(raw))
		assert.Error(t, err, "%s: base URL %q was accepted", name, raw)
	}
}

func TestBaseURLRefusesAPathPrefix(t *testing.T) {
	_, err := New("gitlab.com", BaseURL("https://gitlab.com/gitlab"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relative URL root")

	// The prefix really does land in the owner, which is why it is refused.
	k, err := repokey.Parse("https://gitlab.com/gitlab/acme/pay.git")
	require.NoError(t, err)
	assert.Equal(t, "gitlab/acme", k.Owner)
}

func TestDeploymentRefusesACredentialThatCannotAnswer(t *testing.T) {
	_, err := New("gitlab.com", Deployment(forge.Credential{}))
	assert.Error(t, err, "an empty deployment credential was installed")

	_, err = New("gitlab.com", Deployment(forge.Anonymous()))
	assert.Error(t, err, "an anonymous deployment credential was installed")
}

func TestMembershipIsATypeAndNotANilCheck(t *testing.T) {
	plain, err := New("gitlab.com")
	require.NoError(t, err)
	_, has := plain.(forge.Memberships)
	assert.False(t, has, "a driver with no deployment credential claims it can answer membership")

	withCredential, err := New("gitlab.com", Deployment(forge.Token("deployment")))
	require.NoError(t, err)
	_, has = withCredential.(forge.Memberships)
	assert.True(t, has)
}

func TestWithoutSigningHooksAreUnsupported(t *testing.T) {
	in := newInstance(t)
	for _, opts := range [][]Option{nil, {Deployment(forge.Token(deployToken))}} {
		d := in.driver(t, opts...)
		_, has := d.(forge.Signatures)
		assert.False(t, has)
		registry, err := forge.New(forge.Policy{}, forge.Binding{Host: in.host, Driver: d})
		require.NoError(t, err)
		key := in.keys(t).Admin
		session, err := registry.For(key, forge.Token(goodToken))
		require.NoError(t, err)
		assert.False(t, session.Capabilities().Has(forge.CapPushHooks))
		callback, err := url.Parse("https://build.example.invalid/hooks")
		require.NoError(t, err)
		secret, err := forge.NewSecret()
		require.NoError(t, err)
		var unsupported *forge.UnsupportedError
		_, err = session.RegisterPushHook(context.Background(), key, callback, secret)
		require.ErrorAs(t, err, &unsupported)
		assert.Equal(t, forge.CapPushHooks, unsupported.Capability)
		_, err = registry.VerifyPush(forge.Hook{Key: key, Handle: "1", Scheme: forge.SchemeStandardWebhooks, Secret: secret}, forge.Delivery{})
		require.ErrorAs(t, err, &unsupported)
		assert.Equal(t, forge.CapSignatures, unsupported.Capability)
	}
}

func TestBindRefusesAZeroCredential(t *testing.T) {
	d, err := New("gitlab.com")
	require.NoError(t, err)
	bound, err := d.Bind(forge.Credential{})
	assert.Nil(t, bound)
	assert.ErrorIs(t, err, forge.ErrNoCredential)
}

func TestAddressable(t *testing.T) {
	d, err := New("gitlab.com")
	require.NoError(t, err)

	for _, spelling := range []string{
		"https://gitlab.com/acme/pay.git",
		"https://gitlab.com/acme/team/widget.git",
		"https://gitlab.com/acme/team/sub/deeper/widget.git",
	} {
		k, err := repokey.Parse(spelling)
		require.NoError(t, err)
		assert.NoError(t, d.Addressable(k), "GitLab nests, and %q was refused", spelling)
	}

	rootLevel, err := repokey.Parse("https://gitlab.com/pay.git")
	require.NoError(t, err)
	assert.Empty(t, rootLevel.Owner)
	assert.Error(t, d.Addressable(rootLevel), "a repository at the host root is not a GitLab project")

	for _, spelling := range []string{
		"https://gitlab.com/acme/-/pay.git",
		"https://gitlab.com/acme/-.git",
	} {
		k, err := repokey.Parse(spelling)
		require.NoError(t, err)
		assert.Error(t, d.Addressable(k), "%q uses GitLab's reserved route separator", spelling)
	}

	// A key on another host. The registry routes by host before it ever calls
	// this, so nothing in tau reaches here with one — but this method is
	// exported and answers on its own, and answering "yes, I can address that"
	// about another instance's repository is an answer nobody should get.
	elsewhere, err := repokey.Parse("https://gitlab.example/acme/pay.git")
	require.NoError(t, err)
	assert.Error(t, d.Addressable(elsewhere), "a driver for gitlab.com said it can address a repository on gitlab.example")
}

func TestCredentialHeaders(t *testing.T) {
	seen := make(http.Header)
	record := func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		answer(t, http.StatusOK, `{"id":1,"username":"alice"}`)(w, r)
	}

	// ONE HEADER FOR EVERY KIND: GitLab takes a personal token as a Bearer as
	// well as an OAuth one, and forge.Credential carries no kind to choose by.
	c, _ := stubClient(t, record)
	_, err := c.Whoami(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer stub-token", seen.Get("Authorization"))
	assert.Empty(t, seen.Get("PRIVATE-TOKEN"), "nor in GitLab's other header")
}

func TestAnonymousSessionCarriesNoCredential(t *testing.T) {
	seen := make(http.Header)
	var base string
	d, base := stub(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		answer(t, http.StatusOK, `{"path_with_namespace":"acme/public","http_url_to_repo":"`+base+`/acme/public.git","default_branch":"main","visibility":"public"}`)(w, r)
	})

	bound, err := d.Bind(forge.Anonymous())
	require.NoError(t, err)
	key, err := repokey.Parse(base + "/acme/public.git")
	require.NoError(t, err)
	_, err = bound.Repository(context.Background(), key)
	require.NoError(t, err)
	assert.Empty(t, seen.Get("PRIVATE-TOKEN"))
	assert.Empty(t, seen.Get("Authorization"))
}

func TestWhoamiRefusesAnAnswerWithNoUsername(t *testing.T) {
	c, _ := stubClient(t, answer(t, http.StatusOK, `{"id":1}`))
	who, err := c.Whoami(context.Background())
	assert.Error(t, err)
	assert.Equal(t, forge.Identity{}, who)
}

func TestWhoamiReportsTheUserIdAsAnOpaqueSubject(t *testing.T) {
	c, _ := stubClient(t, answer(t, http.StatusOK, `{"id":4242,"username":"alice","email":"a@b.invalid"}`))
	who, err := c.Whoami(context.Background())
	require.NoError(t, err)
	assert.Equal(t, forge.Identity{Login: "alice", Subject: "4242", Email: "a@b.invalid"}, who)
}

func TestAnAnswerThisDriverWillNotFinishReadingIsAnError(t *testing.T) {
	var host string
	c, key := stubClient(t, func(w http.ResponseWriter, _ *http.Request) {
		// Written without reporting the write: the driver stops reading part way, so the
		// write may fail, which through answer() would race into a t.Errorf.
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"padding":"`+strings.Repeat("x", 5<<20)+`",
			"path_with_namespace":"acme/pay","http_url_to_repo":"http://`+host+`/acme/pay.git",
			"default_branch":"main","visibility":"private"}`)
	})
	host = key.Host

	repo, err := c.Repository(context.Background(), key)
	require.Error(t, err, "this driver read a 5MB answer to the end and believed it: %+v", repo)
	assert.Less(t, len(err.Error()), 2000, "an instance chose how long a tau log line is")
}

func TestAccessTurnsAGraphQLErrorArrayIntoAnError(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK, `{"data":null,"errors":[{"message":"Field 'pushCode' doesn't exist for stub-token"}]}`))
	level, err := c.Access(context.Background(), key)
	require.Error(t, err)
	assert.Equal(t, forge.AccessNone, level)
	assert.Contains(t, err.Error(), "authorization query")
	assert.NotContains(t, err.Error(), "pushCode", "the instance's own words reached the error")
	assert.NotContains(t, err.Error(), "stub-token", "the credential is in an error string")
}

func TestAccessIsBoundToTheKeyItWasAskedAbout(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK, `{"data":{"project":{
		"fullPath":"attacker/pay",
		"userPermissions":{"pushCode":true,"adminProject":true}}}}`))

	got, err := c.Access(context.Background(), key)
	t.Logf("Access(%s) = %v, err = %v", key, got, err)
	assert.False(t, got.CanWrite(),
		"Access answered %v for %q on the strength of the caller's grant on attacker/pay; the answer is not bound to the key that was asked about", got, key)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "attacker",
		"the name the instance resolved to is a string it chose while holding the caller's credential")
}

func TestAccessAcceptsTheAnswerNamingItselfInAnotherCase(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK, `{"data":{"project":{
		"fullPath":"Acme/Pay",
		"userPermissions":{"pushCode":true,"adminProject":false}}}}`))

	got, err := c.Access(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, forge.AccessWrite, got)
}

func TestAccessRefusesAnUnreadableAnswer(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK, `not json`))
	level, err := c.Access(context.Background(), key)
	require.Error(t, err)
	assert.Equal(t, forge.AccessNone, level)
}

func TestOnly401IsAStatementAboutTheCredential(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusUnauthorized, `{"message":"401 Unauthorized"}`))
	_, err := c.Repository(context.Background(), key)
	var refused *forge.CredentialError
	require.ErrorAs(t, err, &refused)
	assert.NotContains(t, err.Error(), "stub-token", "the credential is in an error string")

	// A 403 is not. GitLab answers it for an archived project, a blocked user, a
	// missing scope and a permission the caller genuinely lacks, and a consumer
	// that read it as a verdict would act on the wrong one.
	c, key = stubClient(t, answer(t, http.StatusForbidden, `{"message":"403 Forbidden"}`))
	_, err = c.Repository(context.Background(), key)
	require.Error(t, err)
	assert.NotErrorAs(t, err, &refused)
	var missing *forge.NotFoundError
	assert.NotErrorAs(t, err, &missing)
}

func TestNotFoundIsOnlyForARequestThatNamedARepository(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusNotFound, `{"message":"404 Project Not Found"}`))
	_, err := c.Repository(context.Background(), key)
	var missing *forge.NotFoundError
	assert.ErrorAs(t, err, &missing)

	_, err = c.Create(context.Background(), key, forge.CreateOptions{})
	require.Error(t, err)
	assert.NotErrorAs(t, err, &missing, "a missing namespace read as a missing repository")
}

func TestA404OnAResourceIsNotAVerdictAboutTheProject(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusNotFound, `{"message":"404 Not Found"}`))
	callback, err := url.Parse("https://build.example.invalid/hook")
	require.NoError(t, err)

	var missing *forge.NotFoundError
	for name, err := range map[string]error{
		"removing a deploy key": c.RemoveDeployKey(context.Background(), key, "7"),
		"removing a hook":       c.RemovePushHook(context.Background(), key, "7"),
	} {
		require.Error(t, err, name)
		assert.NotErrorAsf(t, err, &missing,
			"%s answered that the instance serves no repository %q, which is a project that is plainly there", name, key)
	}

	_, err = c.Repository(context.Background(), key)
	require.ErrorAs(t, err, &missing, "a 404 on the project itself is the one thing this taxonomy member is for")
	_, err = c.InstallDeployKey(context.Background(), key, "tau", "ssh-ed25519 AAAA")
	require.ErrorAs(t, err, &missing, "installing under a project that is not there is that project not being there")
	_, err = c.RegisterPushHook(context.Background(), key, callback, "wire")
	require.ErrorAs(t, err, &missing)
}

func TestRepositoryRefusesACloneURLItCannotBuildAKeyFrom(t *testing.T) {
	for name, body := range map[string]string{
		"no clone URL":     `{"path_with_namespace":"acme/pay","default_branch":"main"}`,
		"not a URL":        `{"path_with_namespace":"acme/pay","http_url_to_repo":"not a clone url","default_branch":"main"}`,
		"credential in it": `{"path_with_namespace":"acme/pay","http_url_to_repo":"https://u:p@gitlab.com/acme/pay.git"}`,
	} {
		c, key := stubClient(t, answer(t, http.StatusOK, body))
		repo, err := c.Repository(context.Background(), key)
		assert.Error(t, err, "%s was accepted", name)
		assert.Equal(t, forge.Repository{}, repo, "%s produced a Repository beside its error", name)
	}
}

func TestRepositoryRefusesACloneURLOnAnotherHost(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK,
		`{"path_with_namespace":"acme/pay","http_url_to_repo":"https://gitlab.example/acme/pay.git","default_branch":"main"}`))
	_, err := c.Repository(context.Background(), key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no driver for")
}

func TestRepositoryPassesAnSSHAddressOnAnotherHostThrough(t *testing.T) {
	var host string
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		answer(t, http.StatusOK, `{"path_with_namespace":"acme/pay","http_url_to_repo":"http://`+host+`/acme/pay.git","ssh_url_to_repo":"ssh://git@ssh.acme.example:2222/acme/pay.git","default_branch":"main","visibility":"private"}`)(w, r)
	})
	host = key.Host

	repo, err := c.Repository(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, key, repo.Key, "the key comes from the http clone URL")
	assert.Equal(t, "ssh://git@ssh.acme.example:2222/acme/pay.git", repo.SSH)
	// And HTTPS is HTTPS: a token goes to whichever address is reported there, and
	// swapping the two still keys the same project.
	assert.Equal(t, "http://"+host+"/acme/pay.git", repo.HTTPS,
		"the address a clone spends a credential at is not the one the instance named for it")
	assert.True(t, repo.Private, "an instance visibility that is not public is not public")

	// And here is the consequence, stated rather than hidden: the two addresses
	// of one repository derive two keys.
	fromSSH, err := repokey.Parse(repo.SSH)
	require.NoError(t, err)
	assert.NotEqual(t, repo.Key, fromSSH)
}

func TestInternalVisibilityIsNotPublic(t *testing.T) {
	var host string
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		answer(t, http.StatusOK, `{"http_url_to_repo":"http://`+host+`/acme/pay.git","default_branch":"main","visibility":"internal"}`)(w, r)
	})
	host = key.Host

	repo, err := c.Repository(context.Background(), key)
	require.NoError(t, err)
	assert.True(t, repo.Private)
}

func TestListRefusesAPagerThatNamesItsOwnPageAsSuccessor(t *testing.T) {
	c, _ := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Next-Page", r.URL.Query().Get("page"))
		answer(t, http.StatusOK, `[]`)(w, r)
	})
	err := c.ListRepositories(context.Background(), func(forge.Repository) bool { return true })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "successor")
}

func TestListStopsPagingEventually(t *testing.T) {
	c, _ := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			page = 1
		}
		w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
		answer(t, http.StatusOK, `[]`)(w, r)
	})
	err := c.ListRepositories(context.Background(), func(forge.Repository) bool { return true })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to keep paging")
}

func TestTheListingSkipsWhatThisDriverCouldNotActOn(t *testing.T) {
	var root string
	d, base := stub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[
			{"path_with_namespace":"acme/broken","http_url_to_repo":"nonsense"},
			{"path_with_namespace":"pay","http_url_to_repo":%q},
			{"path_with_namespace":"acme/-/widget","http_url_to_repo":%q},
			{"path_with_namespace":"acme/pay","http_url_to_repo":%q,"default_branch":"main"}
		]`, root+"/pay.git", root+"/acme/-/widget.git", root+"/acme/pay.git")
	})
	root = base
	bound, err := d.Bind(forge.Token("stub-token"))
	require.NoError(t, err)

	var seen []string
	require.NoError(t, bound.(forge.Lister).ListRepositories(context.Background(), func(r forge.Repository) bool {
		assert.NoError(t, d.Addressable(r.Key), "the listing yielded a key this driver's own Addressable refuses")
		seen = append(seen, r.Key.FullName())
		return true
	}))
	assert.Equal(t, []string{"acme/pay"}, seen)
}

func TestCreateRefusesANamespaceThatResolvedSomewhereElse(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK, `{"id":42,"full_path":"acme-holdings"}`))
	_, err := c.Create(context.Background(), key, forge.CreateOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "somewhere other than where it was asked for")
}

func TestCreateRefusesANamespaceWithNoId(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK, `{"full_path":"acme"}`))
	_, err := c.Create(context.Background(), key, forge.CreateOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no id for namespace")
}

func TestCreateSendsTheNamespaceIdAndNoRepositoryId(t *testing.T) {
	var sent map[string]any
	var host string
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			require.NoError(t, decodeInto(r, &sent))
			answer(t, http.StatusCreated, `{"path_with_namespace":"acme/pay","http_url_to_repo":"http://`+host+`/acme/pay.git","default_branch":"main","visibility":"private"}`)(w, r)
			return
		}
		answer(t, http.StatusOK, `{"id":42,"full_path":"acme"}`)(w, r)
	})
	host = key.Host

	repo, err := c.Create(context.Background(), key, forge.CreateOptions{Description: "pay", Private: true})
	require.NoError(t, err)
	assert.Equal(t, key, repo.Key)
	assert.EqualValues(t, 42, sent["namespace_id"])
	assert.Equal(t, "pay", sent["path"])
	assert.Equal(t, "private", sent["visibility"])
	assert.Equal(t, false, sent["initialize_with_readme"], "an initial commit the caller did not ask for")
}

func TestCreateRefusesAProjectMadeSomewhereElse(t *testing.T) {
	var host string
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/namespaces/") {
			answer(t, http.StatusOK, `{"id":42,"full_path":"acme"}`)(w, r)
			return
		}
		answer(t, http.StatusOK, `{"path_with_namespace":"acme/somethingelse",
			"http_url_to_repo":"http://`+host+`/acme/somethingelse.git",
			"default_branch":"main","visibility":"private"}`)(w, r)
	})
	host = key.Host

	repo, err := c.Create(context.Background(), key, forge.CreateOptions{Private: true})
	require.Errorf(t, err, "Create returned a project made somewhere else: %q", repo.Key)
	assert.NotContains(t, err.Error(), "somethingelse",
		"the name the instance made is a string it chose while holding the caller's credential")
}

func TestCreateRefusesAnAnswerItCannotDeriveAKeyFrom(t *testing.T) {
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/namespaces/") {
			answer(t, http.StatusOK, `{"id":42,"full_path":"acme"}`)(w, r)
			return
		}
		answer(t, http.StatusOK, `{"path_with_namespace":"acme/pay","default_branch":"main"}`)(w, r)
	})

	_, err := c.Create(context.Background(), key, forge.CreateOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no clone URL")
}

func TestInstallDeployKeyTakesBackAKeyThatCanPush(t *testing.T) {
	removed := false
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			removed = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		answer(t, http.StatusCreated, `{"id":9,"can_push":true}`)(w, r)
	})

	handle, err := c.InstallDeployKey(context.Background(), key, "tau", "ssh-ed25519 AAAA test")
	require.Error(t, err)
	assert.Empty(t, handle, "a key that can push was handed back as if it were usable")
	assert.True(t, removed, "the writable key was left installed")
	assert.Contains(t, err.Error(), "has been removed")
}

func TestInstallDeployKeySaysSoWhenItCannotTakeOneBack(t *testing.T) {
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			answer(t, http.StatusInternalServerError, `{"message":"500"}`)(w, r)
			return
		}
		answer(t, http.StatusCreated, `{"id":9,"can_push":true}`)(w, r)
	})
	_, err := c.InstallDeployKey(context.Background(), key, "tau", "ssh-ed25519 AAAA test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be removed")
}

func TestAnInstallThatNamesNoHandleIsRefused(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusCreated, `{}`))

	_, err := c.InstallDeployKey(context.Background(), key, "tau", "ssh-ed25519 AAAA test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing could ever remove it")

	callback, err := url.Parse("https://build.example.invalid/hooks")
	require.NoError(t, err)
	_, err = c.RegisterPushHook(context.Background(), key, callback, "wire")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing could ever remove it")
}

func TestRemovingSomethingWithoutItsHandleIsRefused(t *testing.T) {
	var reached int
	c, key := stubClient(t, func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusNoContent)
	})

	require.Error(t, c.RemoveDeployKey(context.Background(), key, ""))
	require.Error(t, c.RemovePushHook(context.Background(), key, ""))
	assert.Equal(t, 0, reached, "an empty handle reached the instance")
}

func TestAHandleIsEscapedRatherThanParsed(t *testing.T) {
	var path string
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	})
	require.NoError(t, c.RemoveDeployKey(context.Background(), key, "..%2f..%2fuser?a#b"))
	assert.Contains(t, path, "..%252f..%252fuser%3Fa%23b",
		"the handle did not arrive as one escaped segment")
	assert.NotContains(t, path, "?", "a handle ended the path and the rest became a query")
}

func TestAHandleThatNamesSomethingElseIsRefused(t *testing.T) {
	for _, handle := range []string{"..", ".", "../..", "a/b", "../../attacker?a#b"} {
		t.Run(handle, func(t *testing.T) {
			var reached int
			c, key := stubClient(t, func(w http.ResponseWriter, _ *http.Request) {
				reached++
				w.WriteHeader(http.StatusNoContent)
			})
			assert.Error(t, c.RemoveDeployKey(context.Background(), key, forge.Handle(handle)))
			assert.Error(t, c.RemovePushHook(context.Background(), key, forge.Handle(handle)))
			assert.Equal(t, 0, reached, "a handle naming something else reached the instance")
		})
	}
}

func TestInstallingOnARepositoryTheCredentialCannotAdministerFails(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusForbidden, `{"message":"403 Forbidden"}`))

	handle, err := c.InstallDeployKey(context.Background(), key, "tau", "ssh-ed25519 AAAA test")
	assert.Error(t, err)
	assert.Empty(t, handle)

	callback, err := url.Parse("https://build.example.invalid/hooks")
	require.NoError(t, err)
	hook, err := c.RegisterPushHook(context.Background(), key, callback, "wire")
	assert.Error(t, err)
	assert.Empty(t, hook)
}

func TestTheWireStringGoesInTheAttributeTheSchemeNames(t *testing.T) {
	type optionCase struct {
		name      string
		options   []Option
		attribute string
		other     string
	}
	for _, testCase := range []optionCase{
		{"standard webhooks", []Option{Signing()}, "signing_token", "token"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var sent map[string]any
			c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, decodeInto(r, &sent))
				answer(t, http.StatusCreated, `{"id":11}`)(w, r)
			}, testCase.options...)

			callback, err := url.Parse("https://build.example.invalid/hooks")
			require.NoError(t, err)
			handle, err := c.RegisterPushHook(context.Background(), key, callback, "the-wire-string")
			require.NoError(t, err)
			assert.Equal(t, forge.Handle("11"), handle)
			assert.Equal(t, "the-wire-string", sent[testCase.attribute], "the wire string was not passed through unaltered")
			assert.Nil(t, sent[testCase.other])
			assert.Equal(t, true, sent["push_events"])
			assert.Equal(t, false, sent["tag_push_events"])
		})
	}
}

func TestTheHookSubscribesToPushesAndNothingElse(t *testing.T) {
	type optionCase struct {
		name      string
		options   []Option
		attribute string
	}
	for _, testCase := range []optionCase{
		{"standard webhooks", []Option{Signing()}, "signing_token"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var sent map[string]any
			c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, decodeInto(r, &sent))
				answer(t, http.StatusCreated, `{"id":11}`)(w, r)
			}, testCase.options...)

			callback, err := url.Parse("https://build.example.invalid/hooks/x")
			require.NoError(t, err)
			_, err = c.RegisterPushHook(context.Background(), key, callback, "the-wire-string")
			require.NoError(t, err)

			assert.Equal(t, map[string]any{
				"url": callback.String(),
				// The one event this deployment has a handler for.
				"push_events": true,
				// A tag push is a different event with the same payload shape,
				// and it is off because tau builds a branch tip.
				"tag_push_events": false,
				// The callback receives a secret-bearing delivery, so the
				// instance verifies the certificate it is delivering to.
				"enable_ssl_verification": true,
				// Written down as false rather than left to GitLab's defaults.
				"merge_requests_events":    false,
				"issues_events":            false,
				"pipeline_events":          false,
				"repository_update_events": false,
				// The wire string, in the attribute the scheme names.
				testCase.attribute: "the-wire-string",
			}, sent, "the hook this driver registers is not the hook it says it registers")
		})
	}
}

func TestDeleteMeansAcceptedAndNotAlwaysGone(t *testing.T) {
	var host string
	c, key := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			answer(t, http.StatusAccepted, `{"message":"202 Accepted"}`)(w, r)
			return
		}
		answer(t, http.StatusOK, `{"http_url_to_repo":"http://`+host+`/acme/pay.git","default_branch":"main","visibility":"private"}`)(w, r)
	})
	host = key.Host

	require.NoError(t, c.Delete(context.Background(), key), "202 Accepted was read as a failure")

	// And the project is still there, which is not this call having failed.
	_, err := c.Repository(context.Background(), key)
	assert.NoError(t, err)
}

func TestCloneAddressCarriesNoCredentialAndNamesTheAccount(t *testing.T) {
	d, err := New("gitlab.example")
	require.NoError(t, err)
	bound, err := d.Bind(forge.Token("secret-token"))
	require.NoError(t, err)
	key, err := repokey.Parse("https://gitlab.example/acme/team/widget.git")
	require.NoError(t, err)

	addresses := bound.(forge.CloneAddresses)
	https, err := addresses.CloneAddress(key, forge.HTTPS)
	require.NoError(t, err)
	assert.Equal(t, "https://gitlab.example/acme/team/widget.git", https.URL)
	assert.Equal(t, "oauth2", https.HTTPUsername)
	assert.NotContains(t, https.URL, "secret-token")

	ssh, err := addresses.CloneAddress(key, forge.SSH)
	require.NoError(t, err)
	assert.Equal(t, "ssh://git@gitlab.example/acme/team/widget.git", ssh.URL)
	assert.Equal(t, "git", ssh.SSHAccount)

	// Both re-derive the key they were built from, which is what repokey
	// refusing a credentialed URL also proves.
	for _, spelling := range []string{https.URL, ssh.URL} {
		got, err := repokey.Parse(spelling)
		require.NoError(t, err)
		assert.Equal(t, key, got)
	}
}

func TestPushReadsOnlyAPush(t *testing.T) {
	d, err := New("gitlab.com", Signing())
	require.NoError(t, err)
	signatures := d.(forge.Signatures)

	// GitLab names the event in prose. A tag push is a different event under a
	// different name, and nothing in tau builds one.
	for _, event := range []string{"", "push", "Tag Push Hook", "Merge Request Hook"} {
		header := make(http.Header)
		if event != "" {
			header.Set("X-Gitlab-Event", event)
		}
		_, err := signatures.Push(forge.VerifiedBody{}, header)
		assert.ErrorIs(t, err, forge.ErrNotAPush, "event %q was read as a push", event)
	}

	pushHeader := make(http.Header)
	pushHeader.Set("X-Gitlab-Event", "Push Hook")
	// forge.VerifiedBody's fields are unexported, so the only non-empty one a
	// driver can ever be handed comes out of Registry.VerifyPush.
	_, err = signatures.Push(forge.VerifiedBody{}, pushHeader)
	require.Error(t, err)
	assert.NotErrorIs(t, err, forge.ErrNotAPush)
}

func TestVerifyPushRejectsPayloadsThatOnlyLookLikePushes(t *testing.T) {
	in := newInstance(t)
	registry, err := forge.New(forge.Policy{},
		forge.Binding{Host: in.host, Driver: in.driver(t, Signing())})
	require.NoError(t, err)

	session, err := registry.For(in.keys(t).Admin, forge.Token(goodToken))
	require.NoError(t, err)
	callback, err := url.Parse("https://build.example.invalid/hooks")
	require.NoError(t, err)
	secret, err := forge.NewSecret()
	require.NoError(t, err)
	hook, err := session.RegisterPushHook(context.Background(), in.keys(t).Admin, callback, secret)
	require.NoError(t, err)

	sign := func(t *testing.T, body string) forge.Delivery {
		t.Helper()
		header, err := forge.Sign(hook.Scheme, secret, []byte(body), "msg_1", time.Now())
		require.NoError(t, err)
		header.Set("X-Gitlab-Event", "Push Hook")
		return forge.Delivery{Header: header, Body: []byte(body)}
	}

	_, err = registry.VerifyPush(hook, sign(t, `{"object_kind":"tag_push","ref":"refs/tags/v1"}`))
	assert.ErrorIs(t, err, forge.ErrNotAPush, "a header saying push over a body saying otherwise was read as a push")

	_, err = registry.VerifyPush(hook, sign(t, `{"object_kind":"push","ref":"refs/heads/main","project":{"git_http_url":"nonsense"}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names no repository")
}

func TestIsActiveMemberNeedsBothHalvesOfTheQuestion(t *testing.T) {
	d, err := New("gitlab.com", Deployment(forge.Token("deployment")))
	require.NoError(t, err)
	members := d.(forge.Memberships)

	_, err = members.IsActiveMember(context.Background(), "", "alice")
	assert.Error(t, err)
	_, err = members.IsActiveMember(context.Background(), "acme", "")
	assert.Error(t, err)
}

func TestIsActiveMemberSaysNoOnlyWhenItKnows(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"no such user":      answer(t, http.StatusOK, `[]`),
		"user with no id":   answer(t, http.StatusOK, `[{"username":"alice"}]`),
		"a blocked account": answer(t, http.StatusOK, `[{"id":7,"state":"blocked"}]`),
	} {
		d, _ := stub(t, handler, Deployment(forge.Token("deployment")))
		ok, err := d.(forge.Memberships).IsActiveMember(context.Background(), "acme", "alice")
		assert.NoError(t, err, "%s", name)
		assert.False(t, ok, "%s", name)
	}

	// A member row that exists and is not an active membership.
	for name, body := range map[string]string{
		"awaiting approval": `{"access_level":30,"state":"awaiting"}`,
		"no access level":   `{"access_level":0,"state":"active"}`,
	} {
		d, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.EscapedPath(), "/members/") {
				answer(t, http.StatusOK, body)(w, r)
				return
			}
			answer(t, http.StatusOK, `[{"id":7,"state":"active"}]`)(w, r)
		}, Deployment(forge.Token("deployment")))
		ok, err := d.(forge.Memberships).IsActiveMember(context.Background(), "acme", "alice")
		assert.NoError(t, err, "%s", name)
		assert.False(t, ok, "%s", name)
	}
}

func TestIsActiveMemberErrorsRatherThanDenying(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"the user lookup was refused":    answer(t, http.StatusForbidden, `{"message":"403 Forbidden"}`),
		"the user lookup was unreadable": answer(t, http.StatusOK, `not json`),
		"the user lookup was not there":  answer(t, http.StatusNotFound, `{"message":"404 Not Found"}`),
	} {
		d, _ := stub(t, handler, Deployment(forge.Token("deployment")))
		ok, err := d.(forge.Memberships).IsActiveMember(context.Background(), "acme", "alice")
		assert.Error(t, err, "%s", name)
		assert.False(t, ok, "%s", name)
	}

	for name, handler := range map[string]http.HandlerFunc{
		"the members lookup was refused": func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.EscapedPath(), "/members/") {
				answer(t, http.StatusForbidden, `{"message":"403 Forbidden"}`)(w, r)
				return
			}
			answer(t, http.StatusOK, `[{"id":7,"state":"active"}]`)(w, r)
		},
		"the members answer was unreadable": func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.EscapedPath(), "/members/") {
				answer(t, http.StatusOK, `not json`)(w, r)
				return
			}
			answer(t, http.StatusOK, `[{"id":7,"state":"active"}]`)(w, r)
		},
	} {
		d, _ := stub(t, handler, Deployment(forge.Token("deployment")))
		ok, err := d.(forge.Memberships).IsActiveMember(context.Background(), "acme", "alice")
		assert.Error(t, err, "%s", name)
		assert.False(t, ok, "%s", name)
	}
}

func TestMembershipSpendsTheDeploymentsCredential(t *testing.T) {
	var seen []string
	d, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if strings.Contains(r.URL.EscapedPath(), "/members/") {
			answer(t, http.StatusOK, `{"access_level":40,"state":"active"}`)(w, r)
			return
		}
		answer(t, http.StatusOK, `[{"id":7,"state":"active"}]`)(w, r)
	}, Deployment(forge.Token("the-deployment-token")))

	ok, err := d.(forge.Memberships).IsActiveMember(context.Background(), "acme", "alice")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, []string{"the-deployment-token", "the-deployment-token"}, seen)
}

func TestTheMembershipWalkKeepsEachHalfInsideOneSegment(t *testing.T) {
	type testCase struct {
		namespace, username string
		refused             bool
	}
	for name, tc := range map[string]testCase{
		"a nested subgroup, which is the ordinary case": {namespace: "acme/team", username: "alice"},
		"a namespace carrying a query and a fragment":   {namespace: "acme?admin=1#x", username: "alice"},
		"a username carrying a query":                   {namespace: "acme", username: "alice?private_token=x"},

		// A "/" in the group is data and in the login is not; a dot segment is refused
		// in either half.
		"a namespace that would walk out of its path": {namespace: "acme/../../projects", username: "alice", refused: true},
		"a namespace that is a dot segment whole":     {namespace: "..", username: "alice", refused: true},
		"a username that would walk out of its path":  {namespace: "acme", username: "../../../user", refused: true},
		"a username that names the collection":        {namespace: "acme", username: ".", refused: true},
		"a username that nests":                       {namespace: "acme", username: "team/alice", refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			var sent []string
			d, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
				sent = append(sent, r.Method+" "+r.URL.EscapedPath()+"?"+r.URL.RawQuery)
				if strings.Contains(r.URL.EscapedPath(), "/members/") {
					answer(t, http.StatusOK, `{"access_level":40,"state":"active"}`)(w, r)
					return
				}
				answer(t, http.StatusOK, `[{"id":7,"state":"active"}]`)(w, r)
			}, Deployment(forge.Token("deployment")))

			_, err := d.(forge.Memberships).IsActiveMember(context.Background(), tc.namespace, tc.username)
			t.Logf("the instance received %q", sent)
			if tc.refused {
				require.Error(t, err, "a half that names something else was asked anyway")
				assert.Emptyf(t, sent, "the refusal is after the request: %q went out carrying the deployment's credential", sent)
				return
			}
			require.NoError(t, err)
			require.Len(t, sent, 2, "the walk is a user lookup and a member read")

			// The user lookup. The username is a query VALUE, so nothing it
			// contains may become a second parameter or a path of its own.
			lookup, err := url.Parse(sent[0][len("GET "):])
			require.NoError(t, err)
			assert.Equal(t, "/api/v4/users", lookup.Path, "the user lookup was addressed somewhere else")
			assert.Equal(t, []string{tc.username}, lookup.Query()["username"],
				"the username did not survive as one query value")
			assert.Len(t, lookup.Query(), 1, "the username became a second parameter: %v", lookup.Query())

			// The member read. GitLab addresses a group by its full path
			// URL-encoded, so every separator in the namespace is %2F and the
			// path this driver built has exactly the segments it wrote.
			read, err := url.Parse(sent[1][len("GET "):])
			require.NoError(t, err)
			assert.Equal(t, "/api/v4/groups/"+url.PathEscape(tc.namespace)+"/members/all/7", read.EscapedPath(),
				"the namespace did not stay inside its own path segment")
			assert.Empty(t, read.RawQuery, "the namespace walked into the query")
		})
	}
}

// routeWithAMethodThatIsNotOne is declared here, at package level, so that the
// route table's own closure test can tell it from the routes this driver ships:
// newRoute records the file it was called from, and a route declared in a test
// is not a request this driver can issue.
var routeWithAMethodThatIsNotOne = newRoute("not a method", "api/v4/projects")

// routeNobodyDeclared is a route value built WITHOUT newRoute, which is the one
// way a path could reach the wire without the sweep having been told about it.
var routeNobodyDeclared = &route{method: http.MethodGet, template: "api/v4/projects", segments: []string{"api", "v4", "projects"}}

func TestRequestsThatCannotBeBuiltAreErrorsAndNotPanics(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK, `{}`))

	// A body nothing can encode.
	err := c.send(context.Background(), routeCreateProject.at(), nil, make(chan int), nil)
	assert.Error(t, err)

	// A method that is not one.
	err = c.send(context.Background(), routeWithAMethodThatIsNotOne.at(), &key, nil, nil)
	assert.Error(t, err)

	// The sweep arms what is in the route table, so a path composed outside it is
	// refused.
	err = c.send(context.Background(), routeNobodyDeclared.at(), &key, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "newRoute")

	// A route given the wrong number of arguments is a path with a hole left in
	// it, and that is an error rather than a request against "{}".
	err = c.send(context.Background(), routeProject.at(), &key, nil, nil)
	require.Error(t, err)
	err = c.send(context.Background(), routeProject.at("a", "b"), &key, nil, nil)
	require.Error(t, err)

	// A query parameter the route does not declare. Declaring them is what
	// makes the sweep able to say a parameter went unexercised, so one that
	// slipped past the declaration would slip past the sweep.
	err = c.send(context.Background(), routeListProjects.at().with(url.Values{"legacy": {"1"}}), nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "legacy")
}

func TestAFaultNamesTheRequestAndNothingTheInstanceWrote(t *testing.T) {
	const words = `{"message":"could not authenticate stub-token, try again","error":"stub-token"}`

	c, key := stubClient(t, answer(t, http.StatusInternalServerError, words))
	_, err := c.Repository(context.Background(), key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GET", "an error must name the method this driver sent")
	assert.Contains(t, err.Error(), "/api/v4/projects/", "an error must name the path this driver built")
	assert.Contains(t, err.Error(), "500 Internal Server Error")
	assert.NotContains(t, err.Error(), "try again", "the instance's own words reached the error")
	assert.NotContains(t, err.Error(), "stub-token", "the credential is in an error string")

	// The needle is anchored on the word before it: the host is 127.0.0.1 with an
	// ephemeral port, which can contain a bare "599".
	c, key = stubClient(t, answer(t, 599, words))
	_, err = c.Repository(context.Background(), key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with 599")
	assert.NotContains(t, err.Error(), "try again")
}

func TestListRefusesAPagerThatNamesSomethingOtherThanAPage(t *testing.T) {
	for _, next := range []string{"could not authenticate stub-token", "1x", "-1", strings.Repeat("9", 10)} {
		c, _ := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Next-Page", next)
			answer(t, http.StatusOK, `[]`)(w, r)
		})
		err := c.ListRepositories(context.Background(), func(forge.Repository) bool { return true })
		require.Error(t, err, "a next page of %q was accepted", next)
		assert.NotContains(t, err.Error(), "stub-token", "the credential is in an error string")
	}
}

func decodeInto(r *http.Request, out any) error {
	return json.NewDecoder(r.Body).Decode(out)
}

func TestAListingRefusesWhatItCouldNotRemoveAndAPagerThatLies(t *testing.T) {
	ctx := context.Background()
	c, key := stubClient(t, answer(t, http.StatusOK, `[{"id":0,"url":"https://x.invalid/hook","title":"tau"}]`))
	_, err := c.ListPushHooks(ctx, key)
	require.ErrorContains(t, err, "with no id")
	_, err = c.ListDeployKeys(ctx, key)
	require.ErrorContains(t, err, "with no id")

	c, key = stubClient(t, answer(t, http.StatusOK, `{"id":1}`))
	_, err = c.ListPushHooks(ctx, key)
	require.Error(t, err, "an object where a list belongs")
	_, err = c.ListDeployKeys(ctx, key)
	require.Error(t, err, "an object where a list belongs")

	for next, says := range map[string]string{"1": "own successor", "two": "not a page number"} {
		c, key = stubClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Next-Page", next)
			w.Write([]byte(`[]`))
		})
		_, err = c.ListPushHooks(ctx, key)
		require.ErrorContains(t, err, says)
	}

	c, key = stubClient(t, answer(t, http.StatusInternalServerError, `{}`))
	_, err = c.ListDeployKeys(ctx, key)
	require.Error(t, err)

	served := 0
	c, key = stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		served++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Next-Page", strconv.Itoa(served+1))
		w.Write([]byte(`[]`))
	})
	_, err = c.ListPushHooks(ctx, key)
	require.ErrorContains(t, err, "refusing to keep paging")
}
