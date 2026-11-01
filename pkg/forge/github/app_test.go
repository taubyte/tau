package github_test

// (false, nil) means only that the app asked and GitHub said not a member;
// everything else is an error, because a caller acts on a false.

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
			// The walk stops there: without the id check the app assertion is spent asking
			// installation 0 for a token.
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

func TestAMembershipQuestionNamesBothHalves(t *testing.T) {
	reg, host := appRegistry(t, answer(http.StatusOK, `{"id":4242}`))
	for _, tc := range [][2]string{{"", member}, {namespace, ""}} {
		ok, err := reg.IsActiveMember(context.Background(), host, tc[0], tc[1])
		require.Error(t, err)
		assert.False(t, ok)
	}
}

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

func TestADeploymentWithNoAppCannotBeAskedAtAll(t *testing.T) {
	reg, host := registryOn(t, answer(http.StatusOK, `{}`))
	ok, err := reg.IsActiveMember(context.Background(), host, namespace, member)
	var un *forge.UnsupportedError
	require.ErrorAs(t, err, &un)
	assert.Equal(t, forge.CapMembership, un.Capability)
	assert.False(t, ok)
}

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
