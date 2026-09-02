package gitlab

// THE CREDENTIAL PIN, in the driver that installs it. forge.PinnedTransport is
// the only thing that attaches this driver's credential and the only thing that
// decides which hops may carry it, and until this file nothing here said so:
// swapping the wrap in httpClient for a bare RoundTripper that sets
// PRIVATE-TOKEN on every hop left the whole tree at exit 0.
//
// WHAT CAN BE ASSERTED THROUGH THE PUBLIC API HERE IS MORE THAN ON GITHUB, and
// the difference is BaseURL. github's base is "https://" + host with no option
// to say otherwise, so its lane found no public seam left and drove the
// transport bind() installed. gitlab's BaseURL documents and ACCEPTS http://,
// and checks only that the URL's host equals the driver's — not that it is
// loopback. So `New("git.acme.internal", BaseURL("http://git.acme.internal"))`
// is a driver an operator can build today, every request it composes is
// cleartext off loopback, and the pin's cleartext rule is the only thing between
// a PRIVATE-TOKEN and the wire. That is a driver call, not a transport call, and
// it is the first test below.
//
// WHAT CANNOT BE ASSERTED THROUGH A DRIVER CALL is the host rule. noRedirect
// stops at the 3xx, so the client never asks the pin for a second request, and
// every URL this driver builds is endpoint() over cfg.base, whose host BaseURL
// already forced to equal cfg.host — no answer from an instance makes it compose
// another. The host rule is therefore driven at the transport httpClient
// actually installed, with the hops a redirect would have produced. That is
// structural rather than behavioural and it is labelled as such below; the
// cleartext rule above it is not.
//
// Nothing here dials: the pin refuses before it calls the transport under it,
// and the transport under it is a recorder that answers in-process.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

const (
	// Long and odd, so that finding one where it must not be is unambiguous.
	pinCallerToken     = "glpat-PINNED-CALLER-8b21f7c4"
	pinDeploymentToken = "glpat-PINNED-DEPLOY-1e6a90dd"

	// An instance an operator names on their own network. It is not loopback,
	// which is the whole point: loopback is the exception the pin makes so that
	// httptest works at all.
	pinHost = "gitlab.acme.internal"
)

// pinRecorder is the transport UNDER the pin — what this driver really put on
// the wire, and what header it carried when it did. It answers in-process, so a
// request that reaches it is a request that was NOT refused.
type pinRecorder struct{ sent []*http.Request }

func (p *pinRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	p.sent = append(p.sent, req.Clone(req.Context()))
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}, nil
}

// TestACleartextInstanceIsNeverSentTheCredential drives EVERY operation this
// driver exposes against a driver built the way BaseURL permits: an instance
// named over http on a host that is not loopback.
//
// It is behaviour through the public API. Not one request may leave, because a
// credential on the wire in the clear is spent — and the recorder under the pin
// is what says whether one did. redirectOps is reused deliberately: the rule is
// a property of the client every method shares, so every method is the unit.
//
// THE DRIVER HOLDS A DEPLOYMENT CREDENTIAL, so that the membership rows in that
// table drive the SECOND client this driver builds. It is a different
// http.Client, built at construction rather than at Bind, and a wrap dropped
// from one of the two is a wrap dropped — the test below this one says the same
// thing once, and this says it per operation.
func TestACleartextInstanceIsNeverSentTheCredential(t *testing.T) {
	rec := &pinRecorder{}
	drv, err := New(pinHost, BaseURL("http://"+pinHost), HTTPTransport(rec), Deployment(forge.Token(pinDeploymentToken)))
	require.NoError(t, err, "BaseURL refused cleartext; if it now does, this test's premise is gone and so is the operator it protects")

	reg, err := forge.New(forge.Policy{AllowUnboundSchemes: true}, forge.Binding{Host: pinHost, Driver: drv})
	require.NoError(t, err)
	key, err := repokey.Parse("https://" + pinHost + "/acme/pay.git")
	require.NoError(t, err)
	sess, err := reg.For(key, forge.Token(pinCallerToken))
	require.NoError(t, err)

	for _, op := range redirectOps {
		t.Run(op.name, func(t *testing.T) {
			got, err := op.call(context.Background(), reg, sess, key)
			t.Logf("%s -> %v, err = %v", op.name, got, err)

			require.Error(t, err, "%s spoke to %s in the clear", op.name, pinHost)
			var leak *forge.CredentialLeakError
			require.ErrorAs(t, err, &leak,
				"%s failed for some other reason, so this says nothing about the credential", op.name)
			assert.Contains(t, leak.To, "http://", "the refusal is about the scheme, and it must name it")
			assert.Empty(t, rec.sent, "the credential went on a cleartext wire: %v", rec.sent)
		})
	}
}

// TestACleartextInstanceIsNeverSentTheDeploymentCredential is the same rule for
// the OTHER credential this driver can hold. It is a second http.Client, built
// at construction rather than at Bind, and a wrap dropped from one of the two is
// a wrap dropped.
func TestACleartextInstanceIsNeverSentTheDeploymentCredential(t *testing.T) {
	rec := &pinRecorder{}
	drv, err := New(pinHost, BaseURL("http://"+pinHost), HTTPTransport(rec), Deployment(forge.Token(pinDeploymentToken)))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{AllowUnboundSchemes: true}, forge.Binding{Host: pinHost, Driver: drv})
	require.NoError(t, err)

	member, err := reg.IsActiveMember(context.Background(), pinHost, "acme", "alice")
	t.Logf("IsActiveMember -> %v, err = %v", member, err)

	assert.False(t, member, "an unanswerable question answered a membership")
	require.Error(t, err)
	var leak *forge.CredentialLeakError
	require.ErrorAs(t, err, &leak, "the membership walk failed for some other reason")
	assert.Empty(t, rec.sent, "the deployment's credential went on a cleartext wire: %v", rec.sent)
}

// pinClients names each http.Client this driver builds, because there are two
// and they are built in two places: one per Bind, one per driver holding a
// deployment credential.
var pinClients = map[string]struct {
	opts   []Option
	client func(*testing.T, forge.Driver) *http.Client
	header string
	value  string
}{
	"the client a caller's token is bound to": {
		client: pinCallerClient,
		header: "PRIVATE-TOKEN",
		value:  pinCallerToken,
	},
	"the client the deployment's own credential spends": {
		opts:   []Option{Deployment(forge.Token(pinDeploymentToken))},
		client: pinDeploymentClient,
		header: "PRIVATE-TOKEN",
		value:  pinDeploymentToken,
	},
}

func pinCallerClient(t *testing.T, d forge.Driver) *http.Client {
	t.Helper()
	bound, err := d.Bind(forge.Token(pinCallerToken))
	require.NoError(t, err)
	return bound.(*client).api
}

func pinDeploymentClient(t *testing.T, d forge.Driver) *http.Client {
	t.Helper()
	m, ok := d.(*memberDriver)
	require.True(t, ok, "a driver given a deployment credential is a different Go type, and it is not this one")
	return m.api
}

// TestEveryClientThisDriverBuildsIsPinnedToTheInstance is the host half of the
// pin, and it is STRUCTURAL: it drives the transport httpClient installed with
// the hops a redirect would have produced, rather than reaching it through a
// driver call. The comment at the top of this file says why there is no longer a
// public seam that reaches it — noRedirect closed the route, and closed it for a
// better reason than the pin had.
//
// It dies three ways: drop the wrap; keep it and bind it to something other than
// the instance; or attach the credential above the pin, into the caller's own
// request, where a retry would carry it on a hop nothing checked.
func TestEveryClientThisDriverBuildsIsPinnedToTheInstance(t *testing.T) {
	for name, tc := range pinClients {
		t.Run(name, func(t *testing.T) {
			rec := &pinRecorder{}
			drv, err := New(pinHost, append(tc.opts, HTTPTransport(rec))...)
			require.NoError(t, err)
			rt := tc.client(t, drv).Transport
			require.NotNil(t, rt, "this client installs no transport, so nothing checks where its credential goes")

			// The hop this driver really makes, and the guard that keeps the
			// refusals below from being vacuous: a pin bound to nothing at all
			// refuses every one of them too.
			allowed, err := http.NewRequest(http.MethodGet, "https://"+pinHost+"/api/v4/user", nil)
			require.NoError(t, err)
			resp, err := rt.RoundTrip(allowed)
			require.NoError(t, err, "the pin refused the one host this driver talks to")
			resp.Body.Close()

			require.Len(t, rec.sent, 1)
			// The pair below is one claim in two halves: the credential is
			// attached PER HOP, INSIDE the pin. The first half alone would pass
			// for a driver that set the header on the request itself and handed
			// the pin a nil attach — the header would still arrive, conformance
			// would still be green, and nothing would decide which hops carry
			// it. The second half alone would pass for a driver that attaches
			// the credential nowhere at all.
			assert.Equal(t, tc.value, rec.sent[0].Header.Get(tc.header),
				"nothing attached the credential to the hop this driver makes")
			assert.Empty(t, allowed.Header.Get(tc.header),
				"the credential was written into the request the driver composed rather than into the hop; a retry of that request would carry it on a hop nothing checked")

			for spelling, to := range map[string]string{
				"another host entirely":                                          "https://elsewhere.example.invalid/api/v4/user",
				"a host that merely ends with this one's name":                   "https://evil-" + pinHost + "/api/v4/user",
				"this very instance in cleartext, which its own BaseURL accepts": "http://" + pinHost + "/api/v4/user",
			} {
				t.Run(spelling, func(t *testing.T) {
					before := len(rec.sent)
					req, err := http.NewRequest(http.MethodGet, to, nil)
					require.NoError(t, err)

					resp, err := rt.RoundTrip(req)
					if resp != nil {
						resp.Body.Close()
					}
					var leak *forge.CredentialLeakError
					require.ErrorAs(t, err, &leak, "the transport this driver installs carried the credential to %s", to)
					assert.Len(t, rec.sent, before, "the request was sent before it was refused")
				})
			}
		})
	}
}
