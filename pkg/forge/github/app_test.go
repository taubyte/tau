package github_test

// Membership is answered with the DEPLOYMENT's credential over three requests,
// and the whole contract is that (false, nil) means one thing and one thing
// only: the app asked, and GitHub said this person is not a member. Everything
// else — the app not installed, a token that could not be minted, a namespace
// that blocked the app, an unreachable host — is an error, because a caller
// acts on a false.

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/github"
)

// appRegistry is a registry whose driver holds the test app credential.
func appRegistry(t *testing.T, h http.Handler) (*forge.Registry, string) {
	t.Helper()
	return registryOn(t, h, github.App(appClientID, testKeyPEM()))
}

// TestOnlyA404OnTheMembershipItselfIsANonMember walks the three requests and
// asserts what each of them being wrong produces. Not one of them is a false.
func TestOnlyA404OnTheMembershipItselfIsANonMember(t *testing.T) {
	for name, tc := range map[string]struct {
		handler    http.HandlerFunc
		wantMember bool
		wantErr    bool
		// wantRequests, when set, is how many requests the walk may make before
		// it gives up. Zero means this case does not say.
		wantRequests int
	}{
		"the app is not installed on this namespace": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			},
			wantErr: true,
		},
		"the installation has no id": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"id":0}`)
			},
			wantErr: true,
			// AND THE WALK STOPS THERE. Without the id check the next request is
			// POST /app/installations/0/access_tokens — this deployment's most
			// privileged credential, the app assertion, spent asking an
			// installation that does not exist to hand back a token. It still
			// ends in an error, which is why deleting the check left the tree at
			// exit 0; the fact worth holding is that the assertion never went out
			// a second time.
			wantRequests: 1,
		},
		"the installation token could not be minted": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/installation") {
					io.WriteString(w, `{"id":4242}`)
					return
				}
				http.Error(w, `{"message":"nope"}`, http.StatusForbidden)
			},
			wantErr: true,
		},
		"the installation token came back empty": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/installation") {
					io.WriteString(w, `{"id":4242}`)
					return
				}
				io.WriteString(w, `{"token":""}`)
			},
			wantErr: true,
		},
		"the namespace blocked the app": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/installation"):
					io.WriteString(w, `{"id":4242}`)
				case strings.HasSuffix(r.URL.Path, "/access_tokens"):
					io.WriteString(w, `{"token":"ghs_x"}`)
				default:
					// A 403 is an operator problem, not a verdict on this user.
					http.Error(w, `{"message":"blocked"}`, http.StatusForbidden)
				}
			},
			wantErr: true,
		},
		"the person is not a member": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/installation"):
					io.WriteString(w, `{"id":4242}`)
				case strings.HasSuffix(r.URL.Path, "/access_tokens"):
					io.WriteString(w, `{"token":"ghs_x"}`)
				default:
					http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
				}
			},
		},
		"the person is a member": {
			handler: func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/installation"):
					io.WriteString(w, `{"id":4242}`)
				case strings.HasSuffix(r.URL.Path, "/access_tokens"):
					io.WriteString(w, `{"token":"ghs_x"}`)
				default:
					io.WriteString(w, `{"state":"active"}`)
				}
			},
			wantMember: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var sent int
			handler := tc.handler
			reg, host := appRegistry(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent++
				handler(w, r)
			}))
			ok, err := reg.IsActiveMember(context.Background(), host, namespace, member)
			if tc.wantRequests > 0 {
				assert.Equal(t, tc.wantRequests, sent,
					"the walk kept going after an answer it had already refused")
			}
			if tc.wantErr {
				require.Error(t, err, "an unanswerable question answered false")
				assert.False(t, ok)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantMember, ok)
		})
	}
}

// TestAnUnacceptedInvitationIsNotMembership. GitHub answers 200 with
// state:"pending" for someone who was invited and never accepted.
func TestAnUnacceptedInvitationIsNotMembership(t *testing.T) {
	f := newFixture(t)
	reg, err := forge.New(forge.Policy{}, forge.Binding{
		Host:   f.Host(),
		Driver: mustDriver(t, f.Host(), github.Transport(f.Transport()), github.App(appClientID, testKeyPEM())),
	})
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		user string
		want bool
	}{
		"an active member":         {member, true},
		"an unaccepted invitation": {pending, false},
		"a stranger":               {nonMember, false},
	} {
		t.Run(name, func(t *testing.T) {
			ok, err := reg.IsActiveMember(context.Background(), f.Host(), namespace, tc.user)
			require.NoError(t, err)
			assert.Equal(t, tc.want, ok)
		})
	}
}

// TestAMembershipQuestionNamesBothHalves.
func TestAMembershipQuestionNamesBothHalves(t *testing.T) {
	reg, host := appRegistry(t, answer(http.StatusOK, `{"id":4242}`))
	for _, tc := range [][2]string{{"", member}, {namespace, ""}} {
		ok, err := reg.IsActiveMember(context.Background(), host, tc[0], tc[1])
		require.Error(t, err)
		assert.False(t, ok)
	}
}

// TestAnUnreachableForgeAnswersNoMembershipQuestion.
func TestAnUnreachableForgeAnswersNoMembershipQuestion(t *testing.T) {
	f := newFixture(t)
	reg, err := forge.New(forge.Policy{}, forge.Binding{
		Host:   f.Host(),
		Driver: mustDriver(t, f.Host(), github.Transport(f.Transport()), github.App(appClientID, testKeyPEM())),
	})
	require.NoError(t, err)

	f.Break()
	defer f.Unbreak()
	ok, err := reg.IsActiveMember(context.Background(), f.Host(), namespace, member)
	require.Error(t, err, "an unreachable forge answered a membership question")
	assert.False(t, ok)
}

// TestADeploymentWithNoAppCannotBeAskedAtAll: *UnsupportedError, never false.
func TestADeploymentWithNoAppCannotBeAskedAtAll(t *testing.T) {
	reg, host := registryOn(t, answer(http.StatusOK, `{}`))
	ok, err := reg.IsActiveMember(context.Background(), host, namespace, member)
	var un *forge.UnsupportedError
	require.ErrorAs(t, err, &un)
	assert.Equal(t, forge.CapMembership, un.Capability)
	assert.False(t, ok)
}

// TestTheAppAssertionIsSignedTheWayGitHubRequires. The fixture verifies it with
// the public half — RS256, this app's issuer, unexpired — so a driver that
// signed with the wrong algorithm or the wrong issuer would be refused here the
// way GitHub refuses it.
func TestTheAppAssertionIsSignedTheWayGitHubRequires(t *testing.T) {
	f := newFixture(t)
	reg, err := forge.New(forge.Policy{}, forge.Binding{
		Host:   f.Host(),
		Driver: mustDriver(t, f.Host(), github.Transport(f.Transport()), github.App(appClientID, testKeyPEM())),
	})
	require.NoError(t, err)

	ok, err := reg.IsActiveMember(context.Background(), f.Host(), namespace, member)
	require.NoError(t, err)
	assert.True(t, ok)

	// A driver holding a DIFFERENT app is refused by the same check.
	other, err := forge.New(forge.Policy{}, forge.Binding{
		Host:   f.Host(),
		Driver: mustDriver(t, f.Host(), github.Transport(f.Transport()), github.App("Iv23liSOMEBODYELSE", testKeyPEM())),
	})
	require.NoError(t, err)
	ok, err = other.IsActiveMember(context.Background(), f.Host(), namespace, member)
	require.Error(t, err, "an assertion issued by another app was accepted")
	assert.False(t, ok)
}

// TestTheInstallationTokenIsTheNarrowestOneTheQuestionNeeds: the fixture
// refuses anything but members:read, so asking for everything the app holds
// would fail here.
func TestTheInstallationTokenIsTheNarrowestOneTheQuestionNeeds(t *testing.T) {
	f := newFixture(t)
	reg, err := forge.New(forge.Policy{}, forge.Binding{
		Host:   f.Host(),
		Driver: mustDriver(t, f.Host(), github.Transport(f.Transport()), github.App(appClientID, testKeyPEM())),
	})
	require.NoError(t, err)
	ok, err := reg.IsActiveMember(context.Background(), f.Host(), namespace, member)
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestAMembershipQuestionAboutAnotherNamespaceIsNotAnswered.
func TestAMembershipQuestionAboutAnotherNamespaceIsNotAnswered(t *testing.T) {
	f := newFixture(t)
	reg, err := forge.New(forge.Policy{}, forge.Binding{
		Host:   f.Host(),
		Driver: mustDriver(t, f.Host(), github.Transport(f.Transport()), github.App(appClientID, testKeyPEM())),
	})
	require.NoError(t, err)
	ok, err := reg.IsActiveMember(context.Background(), f.Host(), "somebody-else", member)
	require.Error(t, err, "a namespace this app is not installed on answered false")
	assert.False(t, ok)
}

func mustDriver(t *testing.T, host string, opts ...github.Option) forge.Driver {
	t.Helper()
	d, err := github.New(host, opts...)
	require.NoError(t, err)
	return d
}

// THE WALK'S TWO HALVES OF THE QUESTION ARE ESCAPED, OR THEY ARE REFUSED, and
// which of the two depends on the string rather than on the caller. A namespace
// and a username are strings a CONSUMER handed this driver — not keys repokey
// vouched for and not addresses Addressable checked — and they are pasted into
// two API paths.
//
// "?" and "#" are characters a path gives meaning to, and escaping is the answer
// to those: they are sent, as themselves, inside one segment. ".." is not a
// character, it is an INSTRUCTION to whoever resolves the path, and escaping is
// no answer at all — url.PathEscape("..") is ".." — so it is refused before the
// deployment's app credential is spent on a request nobody composed. So is a "/",
// because a GitHub login has none and one hole would be filled with two segments.
//
// Unlike the gitlab driver there is no nesting ordinary case here, which is why
// the "/" rows below are refusals on this driver and the group row is an
// ordinary case on that one. Measured: dropping url.PathEscape from both halves
// left the whole forge tree at exit 0, and so did sending "..".
func TestTheMembershipWalkKeepsEachHalfInsideOneSegment(t *testing.T) {
	for name, tc := range map[string]struct {
		namespace, username string
		refused             bool
	}{
		"a namespace carrying a query and a fragment": {namespace: "acme?per_page=1#x", username: "alice"},
		"a username carrying a query":                 {namespace: "acme", username: "alice?access_token=x"},
		"a namespace that would walk out of its path": {namespace: "acme/../../user", username: "alice", refused: true},
		"a username that would walk out of its path":  {namespace: "acme", username: "../../../user", refused: true},
		"a namespace that is a dot segment whole":     {namespace: "..", username: "alice", refused: true},
		"a username that names the collection":        {namespace: "acme", username: ".", refused: true},
		"a namespace that nests":                      {namespace: "acme/team", username: "alice", refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			var sent []string
			reg, host := appRegistry(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = append(sent, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
				switch {
				case strings.HasSuffix(r.URL.EscapedPath(), "/installation"):
					io.WriteString(w, `{"id":4242}`)
				case strings.HasSuffix(r.URL.EscapedPath(), "/access_tokens"):
					io.WriteString(w, `{"token":"ghs_for_the_walk"}`)
				default:
					io.WriteString(w, `{"state":"active"}`)
				}
			}))

			ok, err := reg.IsActiveMember(context.Background(), host, tc.namespace, tc.username)
			t.Logf("the forge received %q", sent)
			if tc.refused {
				require.Error(t, err, "a half that names something else was asked anyway")
				assert.False(t, ok)
				assert.Emptyf(t, sent, "the refusal is after the request: %q went out carrying the deployment's credential", sent)
				return
			}

			require.NoError(t, err)
			assert.True(t, ok)
			require.Len(t, sent, 3, "the walk is an installation lookup, a mint and a member read")

			org := url.PathEscape(tc.namespace)
			assert.Equal(t, []string{
				"/api/v3/orgs/" + org + "/installation?",
				"/api/v3/app/installations/4242/access_tokens?",
				"/api/v3/orgs/" + org + "/memberships/" + url.PathEscape(tc.username) + "?",
			}, sent, "a half of the question walked out of its own path segment")
		})
	}
}

// TestAMembershipAnswerThisDriverCannotReadIsNotAFalse. The 404 shortcut reads
// a STATUS; a failure that never carried one — an unreadable body, a dropped
// connection — must fall through to the error and not to the shortcut.
func TestAMembershipAnswerThisDriverCannotReadIsNotAFalse(t *testing.T) {
	reg, host := appRegistry(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/installation"):
			io.WriteString(w, `{"id":4242}`)
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			io.WriteString(w, `{"token":"ghs_x"}`)
		default:
			io.WriteString(w, `{"state": [ not json`)
		}
	}))
	ok, err := reg.IsActiveMember(context.Background(), host, namespace, member)
	require.Error(t, err)
	assert.False(t, ok)
}
