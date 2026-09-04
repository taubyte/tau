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

func stubClient(t *testing.T, h http.HandlerFunc, opts ...Option) (*client, repokey.Key) {
	t.Helper()
	d, base := stub(t, h, opts...)
	bound, err := d.Bind(forge.Token("stub-token"))
	require.NoError(t, err)
	key, err := repokey.Parse(base + "/acme/pay.git")
	require.NoError(t, err)
	return bound.(*client), key
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

// TestTheDriverKeepsNoCopyOfTheCallersCredential is the github driver's row of
// the same name, which this package did not have.
//
// forge.Credential exists to close EVERY way of rendering key material: Format
// catches every verb and the marshallers fail rather than redact. A driver that
// unwraps one at the door and keeps the string beside its http.Client undoes
// that in one line, and the line buys nothing — the credential is written by a
// closure that captures the parameter and is read nowhere else.
//
// THE DEPLOYMENT'S CREDENTIAL IS A FIELD, deliberately: it belongs to the
// driver and no Session can reach it. It is kept as a forge.Credential and not
// as a string, which is what makes rendering the driver safe, and that is what
// the second half asserts.
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

// ─────────────────────────── construction ───────────────────────────

// A host that is not the spelling repokey derives from a clone URL is a driver
// no key will ever resolve to, and the failure is otherwise silent forever.
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

	// And to the procedure every ordinary instance actually performs, which the
	// zero policy then refuses out loud.
	assert.Equal(t, forge.SchemeSharedToken, d.(*driver).Scheme())
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

// A relative URL root folds itself into every repository's OWNER, because
// repokey has no notion of a path prefix and the instance's own clone URLs carry
// one. Every namespace this driver resolved would then be off by one segment.
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

// A driver without the deployment's credential does not implement
// forge.Memberships AT ALL — it is a different Go type — so "we could not ask"
// can never arrive at a caller as "they are not a member".
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

// The zero forge.Policy is the strict one, and GitLab's decade-old webhook
// procedure binds nothing. A deployment gets that instance's weaker webhook
// authentication only by saying so at the one call site that registers it.
func TestZeroPolicyRefusesAnUnboundScheme(t *testing.T) {
	d, err := New("gitlab.com")
	require.NoError(t, err)

	_, err = forge.New(forge.Policy{}, forge.Binding{Host: "gitlab.com", Driver: d})
	require.Error(t, err, "the zero policy accepted a driver whose hooks bind nothing")
	assert.Contains(t, err.Error(), "does not bind the body")

	_, err = forge.New(forge.Policy{AllowUnboundSchemes: true}, forge.Binding{Host: "gitlab.com", Driver: d})
	assert.NoError(t, err)

	signing, err := New("gitlab.com", Signing())
	require.NoError(t, err)
	_, err = forge.New(forge.Policy{}, forge.Binding{Host: "gitlab.com", Driver: signing})
	assert.NoError(t, err, "the zero policy refused a scheme that binds the body")
}

func TestBindRefusesAZeroCredential(t *testing.T) {
	d, err := New("gitlab.com")
	require.NoError(t, err)
	bound, err := d.Bind(forge.Credential{})
	assert.Nil(t, bound)
	assert.ErrorIs(t, err, forge.ErrNoCredential)
}

// ─────────────────────────── addressability ───────────────────────────

// Addressable is where a forge states its own naming rules. GitLab's are: a
// project lives in a namespace, at any depth, and never under the segment
// GitLab reserves for its own routes.
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

// ─────────────────────────── credentials on the wire ───────────────────────────

func TestCredentialHeaders(t *testing.T) {
	seen := make(http.Header)
	record := func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		answer(t, http.StatusOK, `{"id":1,"username":"alice"}`)(w, r)
	}

	c, _ := stubClient(t, record)
	_, err := c.Whoami(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "stub-token", seen.Get("PRIVATE-TOKEN"))
	assert.Empty(t, seen.Get("Authorization"), "a personal token was also sent as a Bearer")

	// GitLab has two credential headers for two credential kinds, and
	// forge.Credential is one opaque string with no kind — so the deployment
	// says which.
	c, _ = stubClient(t, record, OAuth())
	_, err = c.Whoami(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer stub-token", seen.Get("Authorization"))
	assert.Empty(t, seen.Get("PRIVATE-TOKEN"))
}

// An anonymous session is a real state — GitLab answers public projects to
// nobody in particular — and it is not a dropped token, which is
// forge.ErrNoCredential at Bind.
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

// ─────────────────────────── the failure taxonomy ───────────────────────────

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

// GRAPHQL ANSWERS 200 FOR FAILURES, so the taxonomy's status mapping does not
// reach it and an errors array has to become an error by hand. It must never
// become a level.
// GraphQL answers 200 for failures, so the errors array is the second failure
// channel — and it is prose the instance composed, on the one endpoint reached
// with the caller's credential in a header. It is counted, never quoted.
// TestAnAnswerThisDriverWillNotFinishReadingIsAnError. A response is a
// stranger's bytes even when the stranger is the instance an operator named, and
// reading an unbounded one into memory to find out what it is is the wrong
// order. maxBody is the whole of what stops that, and until this test it was a
// constant with a comment: deleting the LimitReader around the read left the
// tree at exit 0, because nothing here had ever sent a large answer.
//
// The body below is valid JSON describing the project that was asked for, so an
// instance that is read to the end SUCCEEDS — which is what makes the refusal
// the thing being asserted rather than a malformed-input path. The github driver
// holds the same rule the same way.
func TestAnAnswerThisDriverWillNotFinishReadingIsAnError(t *testing.T) {
	var host string
	c, key := stubClient(t, func(w http.ResponseWriter, _ *http.Request) {
		// WRITTEN WITHOUT REPORTING THE WRITE, and that is this case and not a
		// shortcut. The driver is SUPPOSED to stop reading part way through, so
		// the socket is closed under this handler and the write fails — which
		// through the shared answer() helper is a t.Errorf that fires whenever
		// the kernel has not already accepted the whole 5MB, which is a race.
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

// TestAccessIsBoundToTheKeyItWasAskedAbout is the github driver's row of the
// same name, and this driver did not have it.
//
// A VERDICT CARRIES NO NAME, so it is filed under the question's. userPermissions
// describes whatever project the answer describes, and on this driver the
// channel is not the 3xx noRedirect refuses: GraphQL resolves a project
// internally, so a REDIRECT ROUTE left behind by a rename or a transfer is
// followed with no status this driver ever sees. A transferred path is free for
// anyone to re-register the moment it is transferred, which is what turns
// "admin on whatever acme/pay currently points at" into a grant on a path an
// attacker may already hold.
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

// A project whose own path differs only in case is the SAME project: repokey
// lowercases and GitLab does not, so a fold is not a disagreement.
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

// A 404 is "no such repository" only where the request named one. GitLab's
// namespace lookup 404s too, and that is not a statement about a repository.
func TestNotFoundIsOnlyForARequestThatNamedARepository(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusNotFound, `{"message":"404 Project Not Found"}`))
	_, err := c.Repository(context.Background(), key)
	var missing *forge.NotFoundError
	assert.ErrorAs(t, err, &missing)

	_, err = c.Create(context.Background(), key, forge.CreateOptions{})
	require.Error(t, err)
	assert.NotErrorAs(t, err, &missing, "a missing namespace read as a missing repository")
}

// TestA404OnAResourceIsNotAVerdictAboutTheProject is the github driver's row of
// the same name, which this driver did not have — and it failed: a 404 from
// DELETE .../deploy_keys/7 came back as
// `forge: <host> serves no repository "<host>/acme/pay"` about a project that
// is plainly there, because both removals handed the key to the taxonomy.
//
// THE SPLIT IS ASSERTED IN BOTH DIRECTIONS, because the interesting half is not
// that a removal is exempt but that a fetch is not. The two POSTs are on the
// fetch side deliberately: installing under a project that 404s IS the project
// being missing, since the resource does not exist yet to be the subject.
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

// ─────────────────────────── the key round trip ───────────────────────────

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

// An instance behind a proxy that was told the wrong external URL reports clone
// URLs on a host this deployment has no driver for. A key derived from one names
// a repository nothing can ever act on again.
func TestRepositoryRefusesACloneURLOnAnotherHost(t *testing.T) {
	c, key := stubClient(t, answer(t, http.StatusOK,
		`{"path_with_namespace":"acme/pay","http_url_to_repo":"https://gitlab.example/acme/pay.git","default_branch":"main"}`))
	_, err := c.Repository(context.Background(), key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no driver for")
}

// THE SSH ADDRESS IS REPORTED AS THE INSTANCE GAVE IT, unchecked, and this test
// pins that rather than leaving it accidental. A self-managed GitLab routinely
// serves SSH on another name or port; that is a perfectly good clone address and
// a DIFFERENT repokey key, because the host is part of the identity. Inventing
// agreement here would report an address no clone can use.
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
	// AND THE OTHER ADDRESS IS THE OTHER ADDRESS. HTTPS is the one a builder
	// clones with a credential, so a driver that reported the ssh spelling there
	// would send a token to an address nothing derived a key from — and no key
	// check can see it, because the two name the same project. Measured before
	// this line: swapping the two left the whole tree at exit 0.
	assert.Equal(t, "http://"+host+"/acme/pay.git", repo.HTTPS,
		"the address a clone spends a credential at is not the one the instance named for it")
	assert.True(t, repo.Private, "an instance visibility that is not public is not public")

	// And here is the consequence, stated rather than hidden: the two addresses
	// of one repository derive two keys.
	fromSSH, err := repokey.Parse(repo.SSH)
	require.NoError(t, err)
	assert.NotEqual(t, repo.Key, fromSSH)
}

// GitLab has three visibilities and forge.Repository.Private is a bool.
// "internal" is not public, so it reads as private — the safe direction for a
// field that is metadata and never authorization.
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

// ─────────────────────────── listing ───────────────────────────

func TestListRefusesAPagerThatNamesItsOwnPageAsSuccessor(t *testing.T) {
	c, _ := stubClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Next-Page", r.URL.Query().Get("page"))
		answer(t, http.StatusOK, `[]`)(w, r)
	})
	err := c.ListRepositories(context.Background(), func(forge.Repository) bool { return true })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "successor")
}

// A pager that keeps naming a next page would otherwise be an unbounded loop
// inside a call a consumer thinks is one request.
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

// TestTheListingSkipsWhatThisDriverCouldNotActOn is the gitlab row of the
// github driver's TestTheListingPagesAndSkipsWhatItCannotName, and it is here
// because this listing yielded a key its OWN Addressable refuses: an entry at
// the instance root came out Owner "" Name "pay", which is a project no other
// method of this driver would touch and a key nothing downstream could act on.
//
// The entries it must not yield are every way an entry is unnameable — a clone
// URL no key can be derived from, and BOTH of the two things Addressable
// refuses: a project at the instance root and a path segment of "-", which
// GitLab reserves as a route separator. Both of the latter are here because the
// filter must be Addressable itself and not a hand-rolled restatement of half
// of it: with only the root-level entry, replacing the call with
// `repo.Key.Owner == ""` passes this test and still yields acme/-/widget.
// Measured — it did.
//
// The last entry is the one that must still arrive, because a listing that
// skipped everything would pass a test that only counted refusals.
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

// ─────────────────────────── creation ───────────────────────────

// GitLab's namespace lookup FOLLOWS A REDIRECT FROM A RENAMED PATH, so creating
// in "acme" after acme was renamed would silently create in whatever it is
// called now.
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

// ─────────────────────────── deploy keys ───────────────────────────

// Asked for read-only, given a key that can push. Whatever the reason, a
// standing write credential tau issued is the exact thing this interface has no
// parameter for — so it comes straight back off.
// TestCreateRefusesAProjectMadeSomewhereElse is the github driver's
// "asked for X and the forge created Y", which this driver did not have. The
// namespace comparison above it answers a DIFFERENT question — it is made
// before the POST, and GitLab derives a project's path from what it was sent
// under rules of its own, so a project created in the right group can still
// carry the wrong name.
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

// And a create whose answer names no repository at all is the other half of
// the same refusal: there is nothing to compare the key against.
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

// TestAnInstallThatNamesNoHandleIsRefused. A resource the instance made and
// named no id for is a resource nothing can ever remove, which is how v1 ended
// up with standing credentials it issued and forgot.
//
// IT IS THE INSTALL PATH AND ONLY THE INSTALL PATH. This test used to be called
// TestAnEmptyHandleIsRefused, which is the name of a property the REMOVAL path
// has — and did not have here for four rounds, because the name read as if
// somebody had already checked. A name that describes a class instead of the
// call it drives is a name that reports coverage it does not have.
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

// TestRemovingSomethingWithoutItsHandleIsRefused is the github driver's row of
// the same name, which this driver did not have: it pasted an empty handle
// into the route and sent it.
//
// Measured before the fix: DELETE /api/v4/projects/acme%2Fpay/deploy_keys/ and
// .../hooks/ both left the process carrying the caller's credential, both
// returned nil, and tau was told the key and the hook were removed when nothing
// had been. An empty last segment addresses the COLLECTION, so a removal of one
// thing is answered by an answer about all of them.
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

// An opaque handle is escaped, not parsed. Nothing here knows it is a number,
// and escaping is what keeps one inside its own path segment.
//
// THE HANDLE HERE USED TO BE "../../../user/keys/1", AND THAT WAS THE WRONG
// PROPERTY. Escaping it produced "..%2F..%2F..%2Fuser%2Fkeys%2F1", one segment
// on the wire, and this row called that holding — while the ROW IT DID NOT HAVE
// was the one for a handle escaping cannot save. url.PathEscape("..") is "..".
// So the escaping is asserted with a handle that really needs escaping and is
// really a name, and the refusal has a test of its own: see
// TestAHandleThatNamesSomethingElseIsRefused.
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

// TestAHandleThatNamesSomethingElseIsRefused is the row the escaping test above
// could never be, because escaping is not what answers these.
//
// MEASURED ON THE CLEAN TREE: RemoveDeployKey(acme/pay, "..") composed and SENT
//
//	DELETE /api/v4/projects/acme%2Fpay/deploy_keys/..
//
// which RFC 3986 dot-segment removal — Go's own path.Clean, and every conforming
// proxy, gateway and origin — resolves to /api/v4/projects/acme%2Fpay: GitLab's
// delete-project endpoint, reached with the caller's credential, on the project
// tau was asked to tidy. Go's httptest server answered it with a 307 to exactly
// that path. "." resolves to the collection, which is the defect
// TestRemovingSomethingWithoutItsHandleIsRefused was already written against
// with a different spelling, and "a/b" names a resource under a collection
// nobody asked about with no dot in it at all.
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

// A refusal from the instance is the caller's, not swallowed into an empty
// handle.
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

// ─────────────────────────── hooks ───────────────────────────

// One forge, two procedures, two attributes. The driver names the scheme and
// passes the wire string through; it never derives one and never verifies one.
func TestTheWireStringGoesInTheAttributeTheSchemeNames(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		options   []Option
		attribute string
		other     string
	}{
		{"shared token", nil, "token", "signing_token"},
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

// TestTheHookSubscribesToPushesAndNothingElse reads the WHOLE registration body
// and compares it by value, which is the form the github driver's equivalent
// takes and the form this one did not.
//
// Measured before it existed: flipping enable_ssl_verification to false left the
// entire forge tree at exit 0, and so did turning on merge_requests_events,
// issues_events, pipeline_events or repository_update_events, one at a time. The
// two assertions this driver had — push_events and tag_push_events — are two of
// eight fields, and the six they left out are the six that matter most. The
// callback is where a secret-bearing delivery is sent, so an instance told not to
// verify its certificate is an instance told to hand that delivery to whoever
// answers; and every extra event is a delivery this deployment then has to have
// an opinion about, arriving at a callback built for pushes.
//
// WHOLE-BODY EQUALITY IS THE POINT rather than eight assertions. A field ADDED
// here is a value nobody decided, and it fails this too — which per-field
// assertions cannot do, and which is the property that would have caught the
// five flags above without anybody predicting which one.
func TestTheHookSubscribesToPushesAndNothingElse(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		options   []Option
		attribute string
	}{
		{"shared token", nil, "token"},
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
				// Written down as false rather than left out. GitLab's defaults
				// are already these, and relying on a default is how v1 ended up
				// installing a write deploy key on every repository it ever
				// registered.
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

// ─────────────────────────── deletion ───────────────────────────

// ON GITLAB, A NIL FROM Delete MEANS "ACCEPTED FOR DELETION". The API answers
// 202 and the removal is asynchronous, and an instance or group configured for
// delayed deletion keeps the project readable for the retention period. This
// pins that, because the assumption a GitHub-only integration acquires without
// noticing is that a successful delete means a subsequent read fails — and
// acting on that reads a still-live project as a failure of this call.
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

// ─────────────────────────── clone addresses ───────────────────────────

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

// ─────────────────────────── payloads ───────────────────────────

func TestPushReadsOnlyAPush(t *testing.T) {
	d, err := New("gitlab.com")
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

// The two payload cases a driver can only be handed through the core, so they
// are asserted through it: a body whose kind disagrees with its header, and one
// naming no repository.
func TestVerifyPushRejectsPayloadsThatOnlyLookLikePushes(t *testing.T) {
	in := newInstance(t)
	registry, err := forge.New(forge.Policy{AllowUnboundSchemes: true},
		forge.Binding{Host: in.host, Driver: in.driver(t)})
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

// ─────────────────────────── membership ───────────────────────────

func TestIsActiveMemberNeedsBothHalvesOfTheQuestion(t *testing.T) {
	d, err := New("gitlab.com", Deployment(forge.Token("deployment")))
	require.NoError(t, err)
	members := d.(forge.Memberships)

	_, err = members.IsActiveMember(context.Background(), "", "alice")
	assert.Error(t, err)
	_, err = members.IsActiveMember(context.Background(), "acme", "")
	assert.Error(t, err)
}

// (false, nil) is the only definitive non-member. Everything below is one; the
// case after is not.
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

// A 403 means the credential was blocked, which is an operator problem and not a
// verdict on a person — so it is an error and never a false a caller acts on.
//
// A 404 ON THE USER LOOKUP IS IN THAT SET AND NOT IN THE OTHER ONE. GitLab says
// "no such user" with 200 and an empty array; a 404 from that endpoint is the
// endpoint missing or the deployment's token not permitted to reach it, which is
// an operator problem wearing the status the ONE definitive non-member answer
// also wears. Reading it as that answer would deny every member of the group at
// once, and it would look like a policy rather than an outage. Measured: this
// driver already gets it right — fault turns a 404 on a request that named no
// repository into a plain error — and nothing said so, so a shortcut moved up
// one request left the whole tree at exit 0. The 404 that IS the non-member is
// the one on the member read, and the conformance suite holds that.
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

// The deployment's credential is the one that asks, and it is a different token
// from any session's.
func TestMembershipSpendsTheDeploymentsCredential(t *testing.T) {
	var seen []string
	d, _ := stub(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("PRIVATE-TOKEN"))
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

// THE WALK'S TWO HALVES OF THE QUESTION ARE ESCAPED, and on this forge the
// ORDINARY case is the one that needs it: GitLab groups nest, so a namespace of
// "acme/team" is one group and GitLab's group endpoints address it by its full
// path URL-encoded. Unescaped it is /api/v4/groups/acme/team/members/all/7,
// which is a different route on a real instance and not the group that was
// asked about — the same defect Addressable exists to keep out of the
// repository paths, on the one call answered with the deployment's credential.
//
// And a namespace or username tau was handed is a string from a consumer, not a
// key repokey vouched for. "?" and "#" are characters an API path gives meaning
// to, ".." is a segment a server resolves, and either one unescaped is a
// membership question answered by a request nobody composed. The id is not
// checked here: it is this driver's own rendering of an integer.
//
// Measured: dropping the escaping from both halves left the whole forge tree at
// exit 0.
func TestTheMembershipWalkKeepsEachHalfInsideOneSegment(t *testing.T) {
	for name, tc := range map[string]struct {
		namespace, username string
		refused             bool
	}{
		"a nested subgroup, which is the ordinary case": {namespace: "acme/team", username: "alice"},
		"a namespace carrying a query and a fragment":   {namespace: "acme?admin=1#x", username: "alice"},
		"a username carrying a query":                   {namespace: "acme", username: "alice?private_token=x"},

		// A "/" IN THE GROUP IS DATA AND A "/" IN THE LOGIN IS NOT, which is the
		// one place this driver's shape rule differs from the github driver's.
		// A dot segment is refused in either half, because it is not a character
		// escaping can carry: it is an instruction to whoever resolves the path,
		// and url.PathEscape("..") is "..".
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

// ─────────────────────────── plumbing ───────────────────────────

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

	// A route nobody declared. Every request this driver makes is armed by the
	// sweep because the sweep runs over the ROUTE TABLE, so a path composed
	// outside it is a request nothing arms — and it is refused here rather than
	// sent.
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

// A failed request is named by WHAT THIS DRIVER SENT — the method, the path it
// built, and Go's own text for the numeric status. The instance receives the
// caller's credential on every request, so a sentence it gets to choose is a
// sentence it can put that credential in, and this driver renders none of it:
// not the body, not the reason phrase on the status line.
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

	// A status Go has no text for is still a number and nothing else.
	//
	// THE NEEDLE IS ANCHORED ON THE WORD BEFORE IT, and that is not fussiness.
	// This driver renders a failure as `gitlab: <host> answered <method> <path>
	// with <status>`, and <host> here is 127.0.0.1 and an EPHEMERAL PORT — so a
	// bare "599" is satisfied by the port whenever the kernel hands out one
	// containing it. Measured: dropping the number from statusLine's unknown
	// branch passed 5 runs in 2000, which is a real regression escaping about
	// one run in 400.
	c, key = stubClient(t, answer(t, 599, words))
	_, err = c.Repository(context.Background(), key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with 599")
	assert.NotContains(t, err.Error(), "try again")
}

// X-Next-Page goes into the URL of the next request and into the error that
// refuses a pager, so it is checked to be a page number before it is either.
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

// decodeInto reads a request body in a test handler.
func decodeInto(r *http.Request, out any) error {
	return json.NewDecoder(r.Body).Decode(out)
}
